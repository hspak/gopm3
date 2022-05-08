package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/rivo/tview"
)

type ProcessManager struct {
	processes      []*Process
	runningCmds    []*exec.Cmd
	restartRequest []bool
	exitChannel    chan bool
	wg             sync.WaitGroup
	mu             sync.Mutex
	logs           *tview.TextView
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
		log.Fatal(err)
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
	logs := tview.NewTextView().
		SetRegions(true).
		SetDynamicColors(true).
		SetChangedFunc(func() {
			tui.Draw()
		})
	pane.AddItem(logs, 0, 1, false)
	return &ProcessManager{
		processes:      processes,
		restartRequest: make([]bool, 4),
		runningCmds:    make([]*exec.Cmd, 4),
		exitChannel:    make(chan bool),
		logs:           logs,
	}
}

func (pm3 *ProcessManager) Log(format string, v ...any) {
	fmt.Fprintf(pm3.logs, format, v...)
}

func (pm3 *ProcessManager) setupCmd(process *Process, index int) *exec.Cmd {
	pm3.Log("Starting process %s (%s %s)\n", process.cfg.Name, process.cfg.Command, process.cfg.Args)
	cmd := exec.Command(process.cfg.Command, process.cfg.Args)
	pm3.runningCmds[index] = cmd
	writer := io.MultiWriter(process.logFile, tview.ANSIWriter(process.textView))
	cmd.Stdout = writer
	cmd.Stderr = writer
	return cmd
}

func (pm3 *ProcessManager) RunProcess(process *Process, index int) {
	defer pm3.wg.Done()
	cmd := pm3.setupCmd(process, index)
	err := cmd.Run()
	if err != nil {
		pm3.processes[index].logFile.Write([]byte(err.Error()))
	}
	pm3.Log("Process '%s' has exited\n", process.cfg.Name)
	if pm3.restartRequest[index] {
		pm3.mu.Lock()
		pm3.restartRequest[index] = false
		pm3.mu.Unlock()
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
	pm3.Log("No more subprocesses are running!")
	for _, process := range pm3.processes {
		if err := process.logFile.Close(); err != nil {
			pm3.Log("Log file for '%s' failed to be closed properly: %s", process.cfg.Name, err)
		}
	}
	pm3.exitChannel <- true
}

func (pm3 *ProcessManager) Stop(caughtSignal os.Signal) {
	pm3.Log("Sending signal '%s' to all processes\n", caughtSignal)
	for _, cmd := range pm3.runningCmds {
		if err := cmd.Process.Signal(caughtSignal); err != nil {
			pm3.Log(err.Error())
		}
	}
	// TODO: SIGKILL with timeout
}

func (pm3 *ProcessManager) RestartProcess(index int) {
	pm3.runningCmds[index].Process.Signal(syscall.SIGTERM)
	pm3.mu.Lock()
	pm3.restartRequest[index] = true
	pm3.mu.Unlock()
}
