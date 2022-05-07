package main

import (
	"encoding/json"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func setupProcesses() []*Process {
	configFile, err := os.Open("./config.json")
	if err != nil {
		log.Fatal(err)
	}
	config, err := io.ReadAll(configFile)
	if err != nil {
		log.Fatal(err)
	}
	var cfgs []ProcessConfig
	json.Unmarshal(config, &cfgs)
	var processes []*Process
	for _, cfg := range cfgs {
		process := NewProcess(cfg)
		processes = append(processes, process)
	}
	return processes
}

func main() {
	processes := setupProcesses()
	pm3 := NewProcessManager(processes)
	log.Println(pm3)

	go func() {
		pm3.Start()
	}()

	unixSignals := make(chan os.Signal, 1)
	signal.Notify(unixSignals, syscall.SIGINT, syscall.SIGTERM)
	caughtSignal := <-unixSignals
	log.Printf("Caught signal: %s -- exiting gracefully\n", caughtSignal)
	pm3.Stop(caughtSignal)
	<-pm3.exitChannel
	log.Println("All done, bye bye!")
}
