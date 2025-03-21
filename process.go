package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/rivo/tview"
)

type Process struct {
	cfg          ProcessConfig
	logFile      *os.File
	textView     *tview.TextView
	manualAction ManualAction
	hasFocus     bool

	// Buffered writer for log output
	bufferedWriter *BufferedWriter

	// Used to block the restarting of a process.
	// The primary purpose is to enable manaual stop/starts.
	restartBlock chan bool
}

func (p *Process) Cleanup() {
	if p.bufferedWriter != nil {
		p.bufferedWriter.Close()
	}
	if p.logFile != nil {
		p.logFile.Close()
	}
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

func setupProcesses(cfgPath string, tui *tview.Application) []*Process {
	configFile, err := os.Open(cfgPath)
	if err != nil {
		fmt.Println("Missing config file: ./gopm3.config.json")
		os.Exit(1)
	}
	config, err := io.ReadAll(configFile)
	if err != nil {
		panic(err)
	}
	var cfgs []ProcessConfig
	json.Unmarshal(config, &cfgs)
	processes := make([]*Process, len(cfgs), len(cfgs))
	for i, cfg := range cfgs {
		textView := tview.NewTextView()
		processLogsPane := textView.
			SetScrollable(true).
			SetMaxLines(2500).
			SetDynamicColors(true).
			SetChangedFunc(func() {
				tui.Draw()
			})
		process := NewProcess(cfg, processLogsPane)
		processes[i] = process
	}
	return processes
}
