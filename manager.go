package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"

	"github.com/rivo/tview"
)

type ProcessManager struct {
	processes   []*Process
	runningCmds []*exec.Cmd
	exitChannel chan bool
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
		SetChangedFunc(func() {
			tui.Draw()
		})
	return &Process{
		cfg:      processConfig,
		logFile:  logFile,
		textView: textView,
	}
}

func NewProcessManager(processes []*Process) *ProcessManager {
	return &ProcessManager{
		processes:   processes,
		exitChannel: make(chan bool),
	}
}

func (pm3 *ProcessManager) Start() {
	var wg sync.WaitGroup
	for _, process := range pm3.processes {
		wg.Add(1)
		go func(process *Process) {
			defer wg.Done()
			log.Printf("Starting process %s (%s %s)\n", process.cfg.Name, process.cfg.Command, process.cfg.Args)
			cmd := exec.Command(process.cfg.Command, process.cfg.Args)
			pm3.runningCmds = append(pm3.runningCmds, cmd)
			writer := io.MultiWriter(process.logFile, process.textView)
			cmd.Stdout = writer
			cmd.Stderr = writer

			// TODO: Add some way to restart
			if err := cmd.Run(); err != nil {
				log.Printf("Process '%s' has exited: %s", process.cfg.Name, err)
				return
			}
		}(process)
	}
	wg.Wait()
	log.Println("No more subprocesses are running!")
	log.Println("Closing all log files")
	for _, process := range pm3.processes {
		if err := process.logFile.Close(); err != nil {
			log.Printf("Log file for '%s' failed to be closed properly: %s", process.cfg.Name, err)
		}
	}
	pm3.exitChannel <- true
}

func (pm3 *ProcessManager) Stop(caughtSignal os.Signal) {
	log.Printf("Sending signal '%s' to all processes\n", caughtSignal)
	for _, cmd := range pm3.runningCmds {
		if err := cmd.Process.Signal(caughtSignal); err != nil {
			log.Print(err)
		}
	}
	// TODO: SIGKILL with timeout
}
