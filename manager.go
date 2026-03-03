package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rivo/tview"
)

const (
	dockerKillTimeout   = 5 * time.Second
	dockerDetectTimeout = 8 * time.Second
	dockerDetectPoll    = 200 * time.Millisecond
)

type ManualAction int

const (
	ManualNoop ManualAction = iota
	ManualRestart
	ManualStop
)

// TODO: all these arrays are loosely coupled by index.
type ProcessManager struct {
	processes      []*Process
	runningCmds    []*exec.Cmd
	exitChannel    chan bool
	wg             sync.WaitGroup
	mu             sync.Mutex
	logs           *tview.TextView
	logFile        *os.File
	shuttingDown   bool
	stopOnce       sync.Once
	tuiProcessList *tview.List
	disableLogs    bool

	// Serializes "new container diffing" so docker-managed starts don't race.
	dockerStartMu sync.Mutex
}

type ProcessConfig struct {
	Name            string   `json:"name"`
	Command         string   `json:"command"`
	Args            []string `json:"args"`
	RestartDelay    int      `json:"restart_delay"`
	DisableLogs     bool     `json:"disable_logs,omitempty"`
	DockerManaged   bool     `json:"docker_managed,omitempty"`
	UseProcessGroup bool     `json:"use_process_group,omitempty"`
}

func NewProcessManager(processes []*Process, logsPane *tview.TextView, processList *tview.List, processCount int) *ProcessManager {
	homeDir := os.Getenv("HOME")
	logDir := fmt.Sprintf("%s/.gopm3", homeDir)
	logFileName := fmt.Sprintf("%s/%s.log", logDir, "gopm3")
	logFile, err := os.Create(logFileName)
	if err != nil {
		log.Fatal(err)
	}

	disableLogs := false
	if os.Getenv("GOPM3_DISABLE_LOGS") != "" {
		disableLogs = true
	}

	return &ProcessManager{
		processes:      processes,
		runningCmds:    make([]*exec.Cmd, processCount),
		exitChannel:    make(chan bool),
		logs:           logsPane,
		logFile:        logFile,
		shuttingDown:   false,
		tuiProcessList: processList,
		disableLogs:    disableLogs,
	}
}

func (pm3 *ProcessManager) isShuttingDown() bool {
	pm3.mu.Lock()
	defer pm3.mu.Unlock()
	return pm3.shuttingDown
}

func (pm3 *ProcessManager) beginShutdown() {
	pm3.mu.Lock()
	pm3.shuttingDown = true
	pm3.mu.Unlock()
}

func (pm3 *ProcessManager) setRunningCmd(index int, cmd *exec.Cmd) {
	pm3.mu.Lock()
	pm3.runningCmds[index] = cmd
	pm3.mu.Unlock()
}

func (pm3 *ProcessManager) getRunningCmd(index int) *exec.Cmd {
	pm3.mu.Lock()
	defer pm3.mu.Unlock()
	return pm3.runningCmds[index]
}

func (pm3 *ProcessManager) snapshotRunningCmds() []*exec.Cmd {
	pm3.mu.Lock()
	defer pm3.mu.Unlock()

	cmds := make([]*exec.Cmd, len(pm3.runningCmds))
	copy(cmds, pm3.runningCmds)
	return cmds
}

func (pm3 *ProcessManager) writeRestartDecision(index int, value bool) {
	ch := pm3.processes[index].restartBlock

	// Keep only the latest decision and never block shutdown on this channel.
	select {
	case <-ch:
	default:
	}
	select {
	case ch <- value:
	default:
	}
}

func isProcessDoneErr(err error) bool {
	return err == nil || errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH)
}

func (pm3 *ProcessManager) signalCmd(cmd *exec.Cmd, sig syscall.Signal, useGroup bool) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
		return nil
	}

	var err error
	if useGroup {
		err = syscall.Kill(-cmd.Process.Pid, sig) // note the minus sign
	} else {
		err = cmd.Process.Signal(sig)
	}
	if isProcessDoneErr(err) {
		return nil
	}
	return err
}

func sanitizeProcessName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.ReplaceAll(name, " ", "_")
	if name == "" {
		return "process"
	}
	return name
}

func dockerCIDFilePath(processName string) string {
	homeDir := os.Getenv("HOME")
	logDir := fmt.Sprintf("%s/.gopm3", homeDir)
	return fmt.Sprintf("%s/%s.cid", logDir, sanitizeProcessName(processName))
}

func dockerOutputIndicatesGone(output string) bool {
	output = strings.ToLower(output)
	return strings.Contains(output, "no such container") ||
		strings.Contains(output, "is not running")
}

func listDockerContainerIDs() (map[string]struct{}, error) {
	out, err := exec.Command("docker", "ps", "-q", "--no-trunc").Output()
	if err != nil {
		return nil, err
	}

	ids := make(map[string]struct{})
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		ids[line] = struct{}{}
	}
	return ids, nil
}

func latestDockerContainerID() (string, error) {
	out, err := exec.Command("docker", "ps", "-q", "--no-trunc", "--latest").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func findNewDockerContainerID(before map[string]struct{}, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		after, err := listDockerContainerIDs()
		if err == nil {
			for id := range after {
				if before == nil {
					return id, nil
				}
				if _, seen := before[id]; !seen {
					return id, nil
				}
			}
		}
		time.Sleep(dockerDetectPoll)
	}

	// Conservative fallback when diffing doesn't find one.
	return latestDockerContainerID()
}

func (pm3 *ProcessManager) captureDockerContainerID(process *Process, before map[string]struct{}) error {
	if !process.cfg.DockerManaged || process.dockerCIDFile == "" {
		return nil
	}

	containerID, err := findNewDockerContainerID(before, dockerDetectTimeout)
	if err != nil {
		return err
	}
	containerID = strings.TrimSpace(containerID)
	if containerID == "" {
		return os.ErrNotExist
	}

	return os.WriteFile(process.dockerCIDFile, []byte(containerID), 0644)
}

func (pm3 *ProcessManager) killDockerContainer(process *Process) error {
	if !process.cfg.DockerManaged || process.dockerCIDFile == "" {
		return nil
	}

	data, err := os.ReadFile(process.dockerCIDFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	containerID := strings.TrimSpace(string(data))
	if containerID == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), dockerKillTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "kill", containerID)
	output, cmdErr := cmd.CombinedOutput()
	if cmdErr == nil {
		_ = os.Remove(process.dockerCIDFile)
		return nil
	}

	outputText := strings.TrimSpace(string(output))
	if dockerOutputIndicatesGone(outputText) {
		_ = os.Remove(process.dockerCIDFile)
		return nil
	}
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("timed out waiting for docker kill")
	}
	if outputText == "" {
		return cmdErr
	}
	return fmt.Errorf("%w (%s)", cmdErr, outputText)
}

func (pm3 *ProcessManager) setupCmd(process *Process, index int) *exec.Cmd {
	_ = index

	process.dockerCIDFile = ""
	if process.cfg.DockerManaged {
		process.dockerCIDFile = dockerCIDFilePath(process.cfg.Name)
		_ = os.Remove(process.dockerCIDFile)
	}

	cmd := exec.Command(process.cfg.Command, process.cfg.Args...)
	if pm3.disableLogs || process.cfg.DisableLogs {
		cmd.Stdout = process.logFile
		cmd.Stderr = process.logFile
		process.textView.Write([]byte("Logs are disabled, suggest using 'make logs'"))
	} else {
		// Create buffered writers for both stdout and stderr with ~2KB buffer and low-latency flush.
		tviewWriter := NewBufferedWriter(tview.ANSIWriter(process.textView), 2500, 20*time.Millisecond)
		writer := io.MultiWriter(process.logFile, tviewWriter)
		cmd.Stdout = writer
		cmd.Stderr = writer

		// Store the buffered writer to ensure it's closed properly.
		process.bufferedWriter = tviewWriter
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

func (pm3 *ProcessManager) writePid(execCmd *exec.Cmd, name string) {
	if execCmd == nil || execCmd.Process == nil {
		return
	}

	homeDir := os.Getenv("HOME")
	gopm3Dir := fmt.Sprintf("%s/.gopm3", homeDir)
	pidFilename := fmt.Sprintf("%s/%s.pid", gopm3Dir, name)
	pid := []byte(strconv.Itoa(execCmd.Process.Pid))
	if err := os.WriteFile(pidFilename, pid, 0644); err != nil {
		pm3.Log("Failed to write pidFile for %s %s\n", execCmd.Path, execCmd.Args)
	}
}

func (pm3 *ProcessManager) Log(format string, v ...any) {
	writer := io.MultiWriter(pm3.logs, pm3.logFile)
	fmt.Fprintf(writer, format, v...)
}

func (pm3 *ProcessManager) RunProcess(process *Process, index int) {
	defer pm3.wg.Done()
	cmd := pm3.setupCmd(process, index)
	pm3.setRunningCmd(index, cmd)
	pm3.tuiProcessList.SetItemText(index, process.cfg.Name, "")

	var (
		dockerBefore map[string]struct{}
		dockerLocked bool
	)
	if process.cfg.DockerManaged {
		pm3.dockerStartMu.Lock()
		dockerLocked = true
		before, err := listDockerContainerIDs()
		if err != nil {
			pm3.Log("Could not snapshot docker containers for '%s': %v\n", process.cfg.Name, err)
		} else {
			dockerBefore = before
		}
	}

	pm3.Log("Starting process %s (%s %s)\n", process.cfg.Name, process.cfg.Command, process.cfg.Args)
	startErr := cmd.Start()
	if startErr != nil {
		pm3.processes[index].logFile.Write([]byte(startErr.Error()))
		pm3.Log("Failed to start process '%s': %v\n", process.cfg.Name, startErr)
		if dockerLocked {
			pm3.dockerStartMu.Unlock()
		}
	}

	// Write PID to file and, for docker-managed processes, resolve/write CID.
	if startErr == nil {
		pm3.writePid(cmd, process.cfg.Name)
		if process.cfg.DockerManaged {
			if err := pm3.captureDockerContainerID(process, dockerBefore); err != nil {
				pm3.Log("Could not determine docker container ID for '%s': %v\n", process.cfg.Name, err)
			}
		}
		if dockerLocked {
			pm3.dockerStartMu.Unlock()
		}
	}

	osProcess := cmd.Process
	if startErr != nil || osProcess == nil {
		pm3.Log("Process %s has exited unexpectedly\n", process.cfg.Name)
	} else {
		if err := cmd.Wait(); err != nil {
			pm3.Log("Process '%s' has exited: %v\n", process.cfg.Name, err)
		} else {
			pm3.Log("Process '%s' has exited\n", process.cfg.Name)
		}
	}

	processName := pm3.processes[index].cfg.Name
	if !pm3.isShuttingDown() {
		shuttingDown := false
		pm3.mu.Lock()
		manualAction := pm3.processes[index].manualAction
		pm3.mu.Unlock()

		if manualAction != ManualNoop {
			// This "halts" the process so that we have control over when/if a process is restarted.
			// Hack: we use the boolean value to determine whether we're shutting down or not.
			shuttingDown = <-pm3.processes[index].restartBlock

			pm3.mu.Lock()
			pm3.processes[index].manualAction = ManualNoop
			pm3.mu.Unlock()
		} else {
			pm3.tuiProcessList.SetItemText(index, fmt.Sprintf("[yellow](restarting)[white] %s", processName), "")

			// TODO: Triggering a stop during this period will cause one extra restart.
			time.Sleep(time.Duration(pm3.processes[index].cfg.RestartDelay) * time.Millisecond)
		}
		if !shuttingDown && !pm3.isShuttingDown() {
			pm3.Log("Restarting process '%s'\n", process.cfg.Name)
			pm3.processes[index].textView.Write([]byte("====================================================\n"))
			pm3.processes[index].textView.Write([]byte("==================== Restarting ====================\n"))
			pm3.processes[index].textView.Write([]byte("====================================================\n"))
			pm3.wg.Add(1)
			pm3.RunProcess(process, index)
		}
	}

	pm3.tuiProcessList.SetItemText(index, fmt.Sprintf("[red](dead)[white] %s", processName), "")
}

func (pm3 *ProcessManager) Start() {
	for i, process := range pm3.processes {
		pm3.wg.Add(1)
		go pm3.RunProcess(process, i)
	}
	pm3.wg.Wait()
	pm3.Log("No more subprocesses are running!\n")
	for _, process := range pm3.processes {
		process.Cleanup()
	}
	pm3.logFile.Close()
	pm3.exitChannel <- true
}

func (pm3 *ProcessManager) Stop(caughtSignal os.Signal) {
	pm3.stopOnce.Do(func() {
		pm3.beginShutdown()
		pm3.Log("Caught signal: %v, sending SIGTERM to all and waiting %s before SIGKILL\n", caughtSignal, SigKillGracePeriod)

		// Ensure manually stopped processes are unblocked and can finish.
		for i := range pm3.processes {
			pm3.writeRestartDecision(i, true)
			pm3.tuiProcessList.SetItemText(i, fmt.Sprintf("[yellow](stopping)[white] %s", pm3.processes[i].cfg.Name), "")
		}

		for i, cmd := range pm3.snapshotRunningCmds() {
			if err := pm3.killDockerContainer(pm3.processes[i]); err != nil {
				pm3.Log("Error killing docker container for '%s': %v\n", pm3.processes[i].cfg.Name, err)
			}

			// On global shutdown, target process groups first to include descendants.
			if err := pm3.signalCmd(cmd, syscall.SIGTERM, true); err != nil {
				if fallbackErr := pm3.signalCmd(cmd, syscall.SIGTERM, false); fallbackErr != nil {
					pm3.Log("Error stopping process '%s': %v\n", pm3.processes[i].cfg.Name, fallbackErr)
				}
			}
		}

		// If a process can't clean up and terminate in time, force-kill it.
		go func() {
			time.Sleep(SigKillGracePeriod)
			for i, cmd := range pm3.snapshotRunningCmds() {
				if err := pm3.killDockerContainer(pm3.processes[i]); err != nil {
					pm3.Log("Error killing docker container for '%s': %v\n", pm3.processes[i].cfg.Name, err)
				}

				if err := pm3.signalCmd(cmd, syscall.SIGKILL, true); err != nil {
					if fallbackErr := pm3.signalCmd(cmd, syscall.SIGKILL, false); fallbackErr != nil {
						pm3.Log("Error force-killing process '%s': %v\n", pm3.processes[i].cfg.Name, fallbackErr)
					}
				}
			}
		}()
	})
}

func (pm3 *ProcessManager) StopProcess(index int, restart bool) {
	cmd := pm3.getRunningCmd(index)
	if err := pm3.killDockerContainer(pm3.processes[index]); err != nil {
		pm3.Log("Error killing docker container for '%s': %v\n", pm3.processes[index].cfg.Name, err)
	}

	if pm3.processes[index].cfg.UseProcessGroup {
		if err := pm3.signalCmd(cmd, syscall.SIGTERM, true); err != nil {
			pm3.Log("Error stopping process '%s': %v\n", pm3.processes[index].cfg.Name, err)
		}
	} else {
		if err := pm3.signalCmd(cmd, syscall.SIGTERM, false); err != nil {
			pm3.Log("Error stopping process '%s': %v\n", pm3.processes[index].cfg.Name, err)
		}
	}
	if restart {
		pm3.writeRestartDecision(index, false)
	}
}
