package main

import (
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

var (
	Version            = "dev"
	SigKillGracePeriod = 5 * time.Second
)

func usage() {
	fmt.Println(`usage: gopm3

  -h/--help:    show this
  -v/--version: show version
  -c/--config:  pass explicit config path (otherwise assumes config exists in the same directory)`)
}

func argv() string {
	// Assume implicit config path
	if len(os.Args) == 1 {
		return "./gopm3.config.json"
	}

	if len(os.Args) < 2 {
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
	if arg == "-v" || arg == "--version" {
		fmt.Println(Version)
		os.Exit(0)
	}
	if arg == "-c" || arg == "--config" {
		if len(os.Args) != 3 {
			usage()
			os.Exit(1)
		}
		return os.Args[2]
	}

	usage()
	os.Exit(1)
	return "unreachable :)"
}

func main() {
	cfgPath := argv()

	tui := tview.NewApplication()
	mouseState := true
	tui.EnableMouse(mouseState)

	// Top boxes
	logPages := tview.NewFlex()
	logPages.SetBorder(true).SetTitle(" Logs (merged stdout/stderr) (also available in ~/.gopm3/) ")
	processList := tview.NewList().ShowSecondaryText(false)
	processList.SetBorder(true)
	processList.SetTitle("  Processes  ")
	topFlex := tview.NewFlex().AddItem(processList, 0, 1, true).AddItem(logPages, 0, 4, false)

	// Bottom boxes
	bottomFlex := tview.NewFlex()
	bottomFlex.SetBorder(true)
	bottomFlex.SetTitle(" gopm3 logs, hotkeys :: [yellow]<space>[white]: restart process, [yellow]'m'[white]: toggle mouse mode, [yellow]'s'[white]: stop process, [yellow]'esc'[white]: exit ")

	// Merge all the things!
	rootFlex := tview.NewFlex().SetDirection(tview.FlexRow)
	rootFlex.AddItem(topFlex, 0, 4, true).AddItem(bottomFlex, 0, 1, false)
	tui.SetRoot(rootFlex, true)

	// Config parsing
	processes := setupProcesses(cfgPath, tui)
	for _, process := range processes {
		processList.AddItem(process.cfg.Name, "", 0, func() {})
	}

	// Main entrypoint
	pmLogs := tview.NewTextView().
		SetDynamicColors(false).
		SetScrollable(true).
		SetMaxLines(1000).
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

	var focusLock sync.Mutex

	// Swap log views based on highlighted process list
	processList.SetChangedFunc(func(i int, processName, secondary string, hotkey rune) {
		logPages.Clear()
		logPages.AddItem(processes[i].textView, 0, 1, false)

		oldProc := pm3.highlightedProc
		newProc := i
		focusLock.Lock()
		processes[oldProc].hasFocus = false
		processes[newProc].hasFocus = true
		focusLock.Unlock()
	})
	logPages.AddItem(processes[0].textView, 0, 1, false)
	pm3.highlightedProc = 0

	// Support <space> for restarting individual processes
	processList.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		index := processList.GetCurrentItem()
		processName := pm3.processes[index].cfg.Name

		if event.Key() == tcell.KeyRune && event.Rune() == ' ' {
			processList.SetItemText(index, fmt.Sprintf("[yellow](restarting)[white] %s", processName), "")
			pm3.Log("Restarting process '%s'\n", processName)
			pm3.mu.Lock()
			pm3.processes[index].manualAction = ManualRestart
			pm3.mu.Unlock()
			pm3.StopProcess(index, true)
		} else if event.Rune() == 's' {
			processList.SetItemText(index, fmt.Sprintf("[yellow](stopped)[white] %s", processName), "")
			pm3.Log("Stopping process '%s'\n", pm3.processes[index].cfg.Name)
			pm3.mu.Lock()
			pm3.processes[index].manualAction = ManualStop
			pm3.mu.Unlock()
			pm3.StopProcess(index, false)
			return nil
		} else if event.Key() == tcell.KeyLeft || event.Rune() == 'h' {
			tui.SetFocus(logPages.GetItem(0))
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
		pm3.Log("Caught signal: %s sending SIGTERM to all and waiting %d seconds before SIGKILL\n", SigKillGracePeriod, caughtSignal)
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
