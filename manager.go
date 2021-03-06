package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/rivo/tview"
)

type ManualAction int

const (
	ManualNoop ManualAction = iota
	ManualRestart
	ManualStop
)

type ProcessManager struct {
	processes      []*Process
	runningCmds    []*exec.Cmd
	exitChannel    chan bool
	wg             sync.WaitGroup
	mu             sync.Mutex
	logs           *tview.TextView
	logFile        *os.File
	shuttingDown   bool
	tuiProcessList *tview.List
}

type Process struct {
	cfg          ProcessConfig
	logFile      *os.File
	textView     *tview.TextView
	manualAction ManualAction

	// Used to block the restarting of a process.
	// The primary purpose is to enable manaual stop/starts.
	restartBlock chan bool
}

type ProcessConfig struct {
	Name           string   `json:"name"`
	Command        string   `json:"command"`
	Args           []string `json:"args"`
	RestartDelay   int      `json:"restart_delay"`
	NoProcessGroup bool     `json:"use_process_group,omitempty"`
}

func NewProcess(processConfig ProcessConfig, logsPane *tview.TextView) *Process {
	homeDir := os.Getenv("HOME")
	logDir := fmt.Sprintf("%s/.gopm3", homeDir)
	if err := os.MkdirAll(logDir, os.ModePerm); err != nil {
		panic(err)
	}
	logFileName := fmt.Sprintf("%s/%s.log", logDir, processConfig.Name)
	logFile, err := os.Create(logFileName)
	if err != nil {
		log.Fatal(err)
	}

	return &Process{
		cfg:      processConfig,
		logFile:  logFile,
		textView: logsPane,

		// Buffered channel so that we don't block on send.
		restartBlock: make(chan bool, 1),
	}
}

func NewProcessManager(processes []*Process, logsPane *tview.TextView, processList *tview.List, processCount int) *ProcessManager {
	homeDir := os.Getenv("HOME")
	logDir := fmt.Sprintf("%s/.gopm3", homeDir)
	logFileName := fmt.Sprintf("%s/%s.log", logDir, "gopm3")
	logFile, err := os.Create(logFileName)
	if err != nil {
		log.Fatal(err)
	}

	return &ProcessManager{
		processes:      processes,
		runningCmds:    make([]*exec.Cmd, processCount),
		exitChannel:    make(chan bool),
		logs:           logsPane,
		logFile:        logFile,
		shuttingDown:   false,
		tuiProcessList: processList,
	}
}

func setupCmd(process *Process, index int) *exec.Cmd {
	cmd := exec.Command(process.cfg.Command, process.cfg.Args...)
	writer := io.MultiWriter(process.logFile, tview.ANSIWriter(process.textView))
	cmd.Stdout = writer
	cmd.Stderr = writer
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

func (pm3 *ProcessManager) Log(format string, v ...any) {
	writer := io.MultiWriter(pm3.logs, pm3.logFile)
	fmt.Fprintf(writer, format, v...)
}

func (pm3 *ProcessManager) RunProcess(process *Process, index int) {
	defer pm3.wg.Done()
	cmd := setupCmd(process, index)
	pm3.runningCmds[index] = cmd
	pm3.tuiProcessList.SetItemText(index, process.cfg.Name, "")
	pm3.Log("Starting process %s (%s %s)\n", process.cfg.Name, process.cfg.Command, process.cfg.Args)
	if err := cmd.Start(); err != nil {
		pm3.processes[index].logFile.Write([]byte(err.Error()))
	}

	// This value must be an int so we use -1 as an "undefined", invalid state
	// we can check for.
	pgid := -1

	osProcess := pm3.runningCmds[index].Process
	if osProcess == nil {
		pm3.Log("Process %s has exited unexpectedly\n", process.cfg.Name)
	} else {
		pgid, _ = syscall.Getpgid(osProcess.Pid)
		cmd.Wait()
		pm3.Log("Process '%s' has exited\n", process.cfg.Name)
	}

	processName := pm3.processes[index].cfg.Name
	if !pm3.shuttingDown {
		shuttingDown := false
		if pm3.processes[index].manualAction != ManualNoop {
			// This "halts" the process so that we have control over when/if a process is restarted.
			// Hack: we use the boolean value to determine whether we're shutting down or not.
			shuttingDown = <-pm3.processes[index].restartBlock

			pm3.mu.Lock()
			pm3.processes[index].manualAction = ManualNoop
			pm3.mu.Unlock()
		} else {
			pm3.tuiProcessList.SetItemText(index, fmt.Sprintf("[yellow](restarting)[white] %s", processName), "")

			// TODO: Triggering a stop during this period will stop cause 1 more restart.
			time.Sleep(time.Duration(pm3.processes[index].cfg.RestartDelay) * time.Millisecond)
		}
		if !shuttingDown {
			pm3.Log("Restarting process '%s'\n", process.cfg.Name)
			pm3.processes[index].textView.Write([]byte("====================================================\n"))
			pm3.processes[index].textView.Write([]byte("==================== Restarting ====================\n"))
			pm3.processes[index].textView.Write([]byte("====================================================\n"))
			pm3.wg.Add(1)
			pm3.RunProcess(process, index)
		}
	}

	// Doing this for good measure -- I've found some orphaned processes still slip through
	// for those that are supposed to agree to properly handling their own child processes.
	if pgid > 0 {
		syscall.Kill(-pgid, syscall.SIGKILL) // note the minus sign
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
		if err := process.logFile.Close(); err != nil {
			pm3.Log("Log file for '%s' failed to be closed properly: %s\n", process.cfg.Name, err)
		}
	}
	pm3.logFile.Close()
	pm3.exitChannel <- true
}

func (pm3 *ProcessManager) Stop(caughtSignal os.Signal) {
	pm3.Log("Shutting down, sending signal '%s' to all processes\n", caughtSignal)

	// If a process can't clean up and terminate in 5 seconds, we kill it.
	go func() {
		time.Sleep(5 * time.Second)
		for _, cmd := range pm3.runningCmds {
			// TODO: Some error handling for the pgid
			if cmd.Process != nil {
				pgid, _ := syscall.Getpgid(cmd.Process.Pid)
				syscall.Kill(-pgid, syscall.SIGKILL) // note the minus sign
			}
		}
	}()

	for i, cmd := range pm3.runningCmds {
		// Ensure processes are not blocked (manually stopped).
		pm3.processes[i].restartBlock <- true

		if pm3.processes[i].cfg.NoProcessGroup {
			if err := cmd.Process.Signal(caughtSignal); err != nil {
				pm3.Log("Error stopping process '%s': ", pm3.processes[i].cfg.Name)
				pm3.Log(err.Error())
				pm3.Log("\n")
			}
		} else {
			// TODO: Some error handling for the pgid
			if cmd.Process != nil {
				pgid, _ := syscall.Getpgid(cmd.Process.Pid)
				syscall.Kill(-pgid, syscall.SIGTERM) // note the minus sign
			}
		}
	}
}

func (pm3 *ProcessManager) StopProcess(index int, restart bool) {
	if pm3.processes[index].cfg.NoProcessGroup {
		if err := pm3.runningCmds[index].Process.Signal(syscall.SIGTERM); err != nil {
			pm3.Log("Error stopping process '%s': ", pm3.processes[index].cfg.Name)
			pm3.Log(err.Error())
			pm3.Log("\n")
		}
	} else {
		// TODO: Some error handling for the pgid
		if pm3.runningCmds[index].Process != nil {
			pgid, _ := syscall.Getpgid(pm3.runningCmds[index].Process.Pid)
			syscall.Kill(-pgid, syscall.SIGTERM) // note the minus sign
		}
	}
	if restart {
		pm3.processes[index].restartBlock <- false
	}
}
