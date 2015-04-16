// Copyright (c) 2014 Ludovic Fauvet
// Licensed under the MIT license

package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"runtime/pprof"
	"syscall"
	"time"
)

var (
	// Compile time variables
	defaultPidFile string
)

var (
	daemon     bool
	debug      bool
	monitor    bool
	cpuProfile string
	configFile string
	pidFile    string
	runLog     string
)

func init() {
	// Improves perf in 1.1.2 linux/amd64
	runtime.GOMAXPROCS(runtime.NumCPU())

	flag.BoolVar(&daemon, "D", false, "Daemon mode")
	flag.BoolVar(&debug, "debug", false, "Debug mode")
	flag.BoolVar(&monitor, "monitor", true, "Enable the background mirrors monitor")
	flag.StringVar(&cpuProfile, "cpuprofile", "", "write cpu profile to file")
	flag.StringVar(&configFile, "config", "", "Path to the config file")
	flag.StringVar(&pidFile, "p", "", "Path to pid file")
	flag.StringVar(&runLog, "log", "", "File to output logs (default: stderr)")
	flag.Parse()

	LoadConfig()
	ReloadLogs()
}

func main() {

	if debug {
		os.Setenv("DEBUG", "1")
	}

	if cpuProfile != "" {
		f, err := os.Create(cpuProfile)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	if daemon {
		writePidFile()

		// Show our nice welcome logo
		fmt.Println(welcome)

		/* Connect to the database */
		r := NewRedis()
		c := NewCache(r)
		h := HTTPServer(r, c)

		/* Handle SIGNALS */
		k := make(chan os.Signal, 1)
		signal.Notify(k,
			syscall.SIGINT,  // Terminate
			syscall.SIGTERM, // Terminate
			syscall.SIGQUIT, // Stop gracefully
			syscall.SIGHUP,  // Reload config
			syscall.SIGUSR1, // Reopen log files
			syscall.SIGUSR2, // Seamless binary upgrade
		)
		go func() {
			for {
				sig := <-k
				switch sig {
				case syscall.SIGINT:
					fallthrough
				case syscall.SIGTERM:
					removePidFile()
					os.Exit(0)
				case syscall.SIGQUIT:
					if h.SListener != nil {
						log.Notice("Waiting for running tasks for finish...")
						h.SListener.Stop <- true
					} else {
						removePidFile()
						os.Exit(0)
					}
				case syscall.SIGHUP:
					listenAddress := GetConfig().ListenAddress
					if err := ReloadConfig(); err != nil {
						log.Warning("SIGHUP Received: %s\n", err)
					} else {
						log.Notice("SIGHUP Received: Reloading configuration...")
					}
					if GetConfig().ListenAddress != listenAddress {
						h.Restarting = true
						h.SListener.Stop <- true
					}
					h.Reload()
				case syscall.SIGUSR1:
					log.Notice("SIGUSR1 Received: Re-opening logs...")
					ReloadLogs()
				case syscall.SIGUSR2:
					log.Notice("SIGUSR2 Received: Seamless binary upgrade...")
					err := Relaunch(h.SListener.Listener)
					if err != nil {
						log.Error("%s", err)
					} else {
						h.SListener.Stop <- true
					}
				}
			}
		}()

		/* Start the background monitor */
		m := NewMonitor(r, c)
		if monitor {
			go m.monitorLoop()
		}

		// Recover an existing listener (see process.go)
		if l, ppid, err := Recover(); err == nil {
			h.SetListener(l)
			KillParent(ppid)
		}

		/* Finally start the HTTP server */
		var err error
		for {
			err = h.RunServer()
			if h.SListener.Stopped && h.Restarting {
				h.Restarting = false
				continue
			}
			break
		}

		/* Check why the Serve loop exited */
		if h.SListener.Stopped {
			alive := h.SListener.ConnCount.Get()
			if alive > 0 {
				log.Info("%d client(s) still connected…\n", alive)
			}

			m.Terminate()

			/* Wait at most 5 seconds for the clients to disconnect */
			for i := 0; i < 5; i++ {
				/* Get the number of clients still connected */
				alive = h.SListener.ConnCount.Get()
				if alive == 0 {
					break
				}
				time.Sleep(1 * time.Second)
			}

			alive = h.SListener.ConnCount.Get()
			h.Terminate()
			if alive > 0 {
				log.Warning("Server stopped after 5 seconds with %d client(s) still connected.", alive)
			} else {
				log.Notice("Server stopped gracefully.")
			}
			removePidFile()
		} else if err != nil {
			removePidFile()
			m.Terminate()
			h.Terminate()
			log.Fatal(err)
		}
	} else {
		if err := ParseCommands(flag.Args()...); err != nil {
			log.Fatal(err)
		}
	}
}
