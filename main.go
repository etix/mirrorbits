// Copyright (c) 2014-2015 Ludovic Fauvet
// Licensed under the MIT license

package main

import (
	"fmt"
	"github.com/etix/mirrorbits/cli"
	. "github.com/etix/mirrorbits/config"
	"github.com/etix/mirrorbits/core"
	"github.com/etix/mirrorbits/daemon"
	"github.com/etix/mirrorbits/database"
	"github.com/etix/mirrorbits/http"
	"github.com/etix/mirrorbits/logs"
	"github.com/etix/mirrorbits/mirrors"
	"github.com/etix/mirrorbits/process"
	"github.com/op/go-logging"
	"os"
	"os/signal"
	"runtime"
	"runtime/pprof"
	"strings"
	"syscall"
	"time"
)

var (
	log = logging.MustGetLogger("main")
)

func init() {
	// Improves perf in 1.1.2 linux/amd64
	runtime.GOMAXPROCS(runtime.NumCPU())

	LoadConfig()
	logs.ReloadLogs()
}

func main() {

	if core.CpuProfile != "" {
		f, err := os.Create(core.CpuProfile)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	if core.Daemon {
		process.WritePidFile()

		// Show our nice welcome logo
		fmt.Printf(core.Banner+"\n\n", core.VERSION)

		/* Connect to the database */
		r := database.NewRedis()
		r.ConnectPubsub()
		c := mirrors.NewCache(r)
		h := http.HTTPServer(r, c)

		if r.CheckVersion() == database.ErrUpgradeRequired {
			log.Fatalf("Unsupported Redis version, please upgrade to Redis >= %s", database.RedisMinimumVersion)
			return
		}

		/* Start the background monitor */
		m := daemon.NewMonitor(r, c)
		if core.Monitor {
			go m.MonitorLoop()
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
					process.RemovePidFile()
					os.Exit(0)
				case syscall.SIGQUIT:
					m.Stop()
					if h.Listener != nil {
						log.Notice("Waiting for running tasks to finish...")
						h.Stop(5 * time.Second)
					} else {
						process.RemovePidFile()
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
					logs.ReloadLogs()
				case syscall.SIGUSR2:
					log.Notice("SIGUSR2 Received: Seamless binary upgrade...")
					err := process.Relaunch(*h.Listener)
					if err != nil {
						log.Error("Relaunch failed: %s\n", err)
					} else {
						m.Stop()
						h.Stop(10 * time.Second)
					}
				}
			}
		}()

		// Recover an existing listener (see process.go)
		if l, ppid, err := process.Recover(); err == nil {
			h.SetListener(l)
			go func() {
				time.Sleep(500 * time.Millisecond)
				process.KillParent(ppid)
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

		r.Close()

		process.RemovePidFile()

		if err != nil {
			log.Fatal(err)
		} else {
			log.Notice("Server stopped gracefully.")
		}
	} else {
		if err := cli.ParseCommands(core.Args()...); err != nil {
			log.Fatal(err)
		}
	}
	os.Exit(0)
}
