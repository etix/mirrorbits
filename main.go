// Copyright (c) 2014-2015 Ludovic Fauvet
// Licensed under the MIT license

package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"runtime/pprof"
	"strings"
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
		fmt.Printf(welcome+"\n\n", VERSION)

		/* Connect to the database */
		r := NewRedis()
		r.ConnectPubsub()
		c := NewCache(r)
		h := HTTPServer(r, c)

		defer r.Close()

		/* Start the background monitor */
		m := NewMonitor(r, c)
		if monitor {
			go m.monitorLoop()
		}

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
					m.Stop()
					if h.Listener != nil {
						log.Notice("Waiting for running tasks to finish...")
						h.Stop(5 * time.Second)
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
						h.Stop(1 * time.Second)
					}
					h.Reload()
				case syscall.SIGUSR1:
					log.Notice("SIGUSR1 Received: Re-opening logs...")
					ReloadLogs()
				case syscall.SIGUSR2:
					log.Notice("SIGUSR2 Received: Seamless binary upgrade...")
					err := Relaunch(*h.Listener)
					if err != nil {
						log.Error("%s", err)
					} else {
						m.Stop()
						h.Stop(10 * time.Second)
					}
				}
			}
		}()

		// Recover an existing listener (see process.go)
		if l, ppid, err := Recover(); err == nil {
			h.SetListener(l)
			go func() {
				time.Sleep(500 * time.Millisecond)
				KillParent(ppid)
			}()
		}

		/* Finally start the HTTP server */
		var err error
		for {
			err = h.RunServer()
			if h.Restarting {
				h.Restarting = false
				continue
			}
			// This check is ugly but there's still no way to detect this error by type
			if err != nil && strings.Contains(err.Error(), "use of closed network connection") {
				// This error is expected during a graceful shutdown
				err = nil
			}
			break
		}

		log.Debug("Waiting for monitor termination")
		m.Wait()

		log.Debug("Terminating server")
		h.Terminate()

		removePidFile()

		if err != nil {
			log.Fatal(err)
		} else {
			log.Notice("Server stopped gracefully.")
		}
	} else {
		if err := ParseCommands(flag.Args()...); err != nil {
			log.Fatal(err)
		}
	}
	os.Exit(0)
}
