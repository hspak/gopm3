package main

import (
	"bufio"
	"log"
	"os"
	"os/exec"
	"sync"
)

type ProcessManager struct {
	processes   []Process
	runningCmds []*exec.Cmd
	exitChannel chan bool
}

type Process struct {
	Name         string `json:"name"`
	Command      string `json:"command"`
	Args         string `json:"args"`
	RestartDelay int    `json:"restart_delay"`
}

func NewProcessManager(processes []Process) *ProcessManager {
	return &ProcessManager{
		processes: processes,
		exitChannel: make(chan bool, 1),
	}
}

func (pm3 *ProcessManager) Start() {
	var wg sync.WaitGroup
	for _, process := range pm3.processes {
		wg.Add(1)
		go func(process Process) {
			defer wg.Done()
			log.Printf("Starting processz %s (%s %s)\n", process.Name, process.Command, process.Args)
			cmd := exec.Command(process.Command, process.Args)
			pm3.runningCmds = append(pm3.runningCmds, cmd)
			stdout, err := cmd.StderrPipe()
			if err != nil {
				log.Println("fail")
				log.Print(err)
				return
			}
			if err := cmd.Start(); err != nil {
				log.Println("Failed to start cmd", err)
				log.Print(err)
				return
			}
			reader := bufio.NewReader(stdout)
			line, err := reader.ReadString('\n')
			for err == nil {
				log.Print(line)
				line, err = reader.ReadString('\n')
			}
		}(process)
	}
	wg.Wait()
	log.Println("No more subprocesses are running!")
	pm3.exitChannel <- true
}

func (pm3 *ProcessManager) Stop(caughtSignal os.Signal) {
	log.Printf("Sending signal %s to all processes\n", caughtSignal)
	for _, cmd := range pm3.runningCmds {
		if err := cmd.Process.Signal(caughtSignal); err != nil {
			log.Print(err)
		}
	}
}
