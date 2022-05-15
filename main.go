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

var (
	Version = "dev"
)

func setupProcesses(tui *tview.Application) []*Process {
	configFile, err := os.Open("./gopm3.config.json")
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
		processLogsPane := tview.NewTextView().
			SetRegions(true).
			SetScrollable(true).
			SetDynamicColors(true).
			SetChangedFunc(func() {
				tui.Draw()
			})
		process := NewProcess(cfg, processLogsPane)
		processes = append(processes, process)
	}
	return processes
}

func usage() {
	fmt.Println("usage: gopm3 [-h/--help/-v/--version]")
}

func argv() {
	if len(os.Args) == 1 {
		return
	}

	if len(os.Args) > 2 {
		usage()
		os.Exit(1)
	}

	arg := os.Args[1]
	if arg == "-h" || arg == "--help" {
		usage()
		os.Exit(0)
	}
	if arg == "-v" || arg == "--version" {
		fmt.Println(Version)
		os.Exit(0)
	}
}

func main() {
	argv()

	tui := tview.NewApplication()
	mouseState := true
	tui.EnableMouse(mouseState)

	// Top boxes
	logPages := tview.NewFlex()
	logPages.SetBorder(true).SetTitle("Logs (merged stdout/stderr) (also available in ~/.gopm3/)")
	processList := tview.NewList().ShowSecondaryText(false)
	processList.SetBorder(true)
	processList.SetTitle("Processes")
	topFlex := tview.NewFlex().AddItem(processList, 0, 1, true).AddItem(logPages, 0, 4, false)

	// Bottom boxes
	bottomFlex := tview.NewFlex()
	bottomFlex.SetBorder(true)
	bottomFlex.SetTitle("gopm3 logs")

	// Merge all the things!
	rootFlex := tview.NewFlex().SetDirection(tview.FlexRow)
	rootFlex.AddItem(topFlex, 0, 4, true).AddItem(bottomFlex, 0, 1, false)
	tui.SetRoot(rootFlex, true)

	// Config parsing
	processes := setupProcesses(tui)
	for _, process := range processes {
		processList.AddItem(process.cfg.Name, "", 0, func() {})
	}

	// Main entrypoint
	pmLogs := tview.NewTextView().
		SetRegions(true).
		SetDynamicColors(true).
		SetScrollable(true).
		SetChangedFunc(func() {
			tui.Draw()
		})
	bottomFlex.AddItem(pmLogs, 0, 1, false)
	pm3 := NewProcessManager(processes, pmLogs, processList, len(processes))
	go func() {
		pm3.Start()
	}()

	rootFlex.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Rune() == 'm' {
			mouseState = !mouseState
			tui.EnableMouse(mouseState)
			pm3.Log("Mouse State: %v\n", mouseState)
		}
		return event
	})

	// Swap log views based on highlighted process list
	processList.SetChangedFunc(func(i int, processName, secondary string, hotkey rune) {
		logPages.Clear()
		logPages.AddItem(processes[i].textView, 0, 1, true)
	})
	logPages.AddItem(processes[0].textView, 0, 1, true)

	// Support <space> for restarting individual processes
	processList.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyRune && event.Rune() == ' ' {
			index := processList.GetCurrentItem()
			processList.SetItemText(index, "--- restarting ---", "")
			pm3.Log("Restarting process '%s'\n", pm3.processes[index].cfg.Name)
			pm3.mu.Lock()
			pm3.processes[index].manualRestart = true
			pm3.mu.Unlock()
			pm3.RestartProcess(index)
		} else if event.Key() == tcell.KeyLeft || event.Rune() == 'h' {
			tui.SetFocus(logPages.GetItem(0))
			pm3.Log("Count %d\n", logPages.GetItemCount())
			return nil
		} else if event.Key() == tcell.KeyRight || event.Rune() == 'l' {
			tui.SetFocus(logPages.GetItem(0))
			return nil
		}
		return event
	})

	logPages.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyLeft || event.Rune() == 'h' {
			tui.SetFocus(processList)
			return nil
		} else if event.Key() == tcell.KeyRight || event.Rune() == 'l' {
			tui.SetFocus(processList)
			return nil
		}
		return event
	})

	// Kill with both ESC or Ctrl+c
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
