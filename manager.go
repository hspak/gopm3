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

type ProcessManager struct {
	processes    []*Process
	runningCmds  []*exec.Cmd
	exitChannel  chan bool
	wg           sync.WaitGroup
	mu           sync.Mutex
	logs         *tview.TextView
	logFile      *os.File
	shuttingDown bool
}

type Process struct {
	cfg      ProcessConfig
	logFile  *os.File
	textView *tview.TextView
}

type ProcessConfig struct {
	Name         string `json:"name"`
	Command      string `json:"command"`
	Args         string `json:"args"`
	RestartDelay int    `json:"restart_delay"`
}

func NewProcess(processConfig ProcessConfig, tui *tview.Application) *Process {
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
	textView := tview.NewTextView().
		SetRegions(true).
		SetDynamicColors(true).
		SetChangedFunc(func() {
			tui.Draw()
		})
	return &Process{
		cfg:      processConfig,
		logFile:  logFile,
		textView: textView,
	}
}

func NewProcessManager(processes []*Process, tui *tview.Application, pane *tview.Flex) *ProcessManager {
	homeDir := os.Getenv("HOME")
	logDir := fmt.Sprintf("%s/.gopm3", homeDir)
	logFileName := fmt.Sprintf("%s/%s.log", logDir, "gopm3")
	logFile, err := os.Create(logFileName)
	if err != nil {
		log.Fatal(err)
	}
	logs := tview.NewTextView().
		SetRegions(true).
		SetDynamicColors(true).
		SetChangedFunc(func() {
			tui.Draw()
		})
	pane.AddItem(logs, 0, 1, false)
	return &ProcessManager{
		processes:    processes,
		runningCmds:  make([]*exec.Cmd, 4),
		exitChannel:  make(chan bool),
		logs:         logs,
		logFile:      logFile,
		shuttingDown: false,
	}
}

func setupCmd(process *Process, index int) *exec.Cmd {
	cmd := exec.Command(process.cfg.Command, process.cfg.Args)
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
	pm3.Log("Starting process %s (%s %s)\n", process.cfg.Name, process.cfg.Command, process.cfg.Args)
	if err := cmd.Run(); err != nil {
		pm3.processes[index].logFile.Write([]byte(err.Error()))
	}
	pm3.Log("Process '%s' has exited\n", process.cfg.Name)

	if !pm3.shuttingDown {
		time.Sleep(time.Duration(pm3.processes[index].cfg.RestartDelay) * time.Millisecond)
		pm3.wg.Add(1)
		pm3.Log("Restarting process '%s'\n", process.cfg.Name)
		pm3.processes[index].textView.Write([]byte("====================================================\n"))
		pm3.processes[index].textView.Write([]byte("==================== Restarting ====================\n"))
		pm3.processes[index].textView.Write([]byte("====================================================\n"))
		pm3.RunProcess(process, index)
	}
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
	for i, cmd := range pm3.runningCmds {
		pgid, err := syscall.Getpgid(cmd.Process.Pid)
		if err == nil {
			syscall.Kill(-pgid, syscall.SIGTERM) // note the minus sign
		} else {
			pm3.Log("Failed to kill process group for '%s'", pm3.processes[i].cfg.Name)
			pm3.Log("Attempting a regular process killing for '%s'", pm3.processes[i].cfg.Name)
			if err := cmd.Process.Signal(caughtSignal); err != nil {
				pm3.Log("Error stopping process '%s': ", pm3.processes[i].cfg.Name)
				pm3.Log(err.Error())
				pm3.Log("\n")
			}
		}
	}
	// TODO: SIGKILL with timeout
}

func (pm3 *ProcessManager) RestartProcess(index int) {
	// TODO: Some error handling for the pgid
	pgid, _ := syscall.Getpgid(pm3.runningCmds[index].Process.Pid)
	syscall.Kill(-pgid, syscall.SIGTERM) // note the minus sign
}
