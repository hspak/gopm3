package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func setupProcesses(tui *tview.Application) []*Process {
	configFile, err := os.Open("./config.json")
	if err != nil {
		panic(err)
	}
	config, err := io.ReadAll(configFile)
	if err != nil {
		panic(err)
	}
	var cfgs []ProcessConfig
	json.Unmarshal(config, &cfgs)
	var processes []*Process
	for _, cfg := range cfgs {
		process := NewProcess(cfg, tui)
		processes = append(processes, process)
	}
	return processes
}

func main() {
	tui := tview.NewApplication()
	tui.EnableMouse(true)
	rootFlex := tview.NewFlex().SetDirection(tview.FlexRow)
	pmLogPane := tview.NewFlex()
	pmLogPane.SetBorder(true)
	pmLogPane.SetTitle("gopm3 logs")
	logPages := tview.NewFlex()
	logPages.SetBorder(true).SetTitle("Logs (merged stdout/stderr)")
	processes := setupProcesses(tui)

	processList := tview.NewList().ShowSecondaryText(false)
	processList.SetTitle("Processes")

	for _, process := range processes {
		processList.AddItem(process.cfg.Name, "", 0, func() {})
	}

	processList.SetBorder(true).SetTitle(processes[0].cfg.Name)
	flex := tview.NewFlex().AddItem(processList, 0, 1, true).AddItem(logPages, 0, 4, false)
	rootFlex.AddItem(flex, 0, 4, true).AddItem(pmLogPane, 0, 1, false)
	tui.SetRoot(rootFlex, true)

	pm3 := NewProcessManager(processes, tui, pmLogPane)
	go func() {
		pm3.Start()
	}()

	processList.SetChangedFunc(func(i int, processName, secondary string, hotkey rune) {
		logPages.Clear()
		logPages.AddItem(processes[i].textView, 0, 1, true)
	})
	logPages.AddItem(processes[0].textView, 0, 1, true)

	processList.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Rune() {
		case ' ':
			index := processList.GetCurrentItem()
			pm3.Log("Restarting process '%s'\n", pm3.processes[index].cfg.Name)
			pm3.mu.Lock()
			pm3.processes[index].manualRestart = true
			pm3.mu.Unlock()
			pm3.RestartProcess(index)
		}
		return event
	})

	tui.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc || event.Key() == tcell.KeyCtrlC {
			pm3.shuttingDown = true
			pm3.Stop(syscall.SIGTERM)
		}
		return event
	})

	go func() {
		unixSignals := make(chan os.Signal, 1)
		signal.Notify(unixSignals, syscall.SIGINT, syscall.SIGTERM)
		caughtSignal := <-unixSignals
		pm3.Log("Caught signal: %s -- exiting gracefully\n", caughtSignal)
		pm3.Stop(caughtSignal)
	}()

	go func() {
		if err := tui.Run(); err != nil {
			panic(err)
		}
	}()

	fmt.Println("Waiting for things to end...")
	<-pm3.exitChannel
	tui.Stop()
	fmt.Println("Bye Bye!")
}
