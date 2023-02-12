package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/rivo/tview"
)

type Process struct {
	cfg          ProcessConfig
	logFile      *os.File
	textView     *tview.TextView
	manualAction ManualAction

	// Used to block the restarting of a process.
	// The primary purpose is to enable manaual stop/starts.
	restartBlock chan bool
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
	var processes []*Process
	for _, cfg := range cfgs {
		textView := tview.NewTextView()
		processLogsPane := textView.
			SetScrollable(true).
			SetMaxLines(2500).
			SetDynamicColors(false).
			SetChangedFunc(func() {
				tui.Draw()
			})
		process := NewProcess(cfg, processLogsPane)
		processes = append(processes, process)
	}
	return processes
}
