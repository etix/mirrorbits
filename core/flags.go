// Copyright (c) 2014-2019 Ludovic Fauvet
// Licensed under the MIT license

package core

import (
	"flag"
	"os"
)

var (
	Daemon      bool
	Debug       bool
	Monitor     bool
	ConfigFile  string
	CpuProfile  string
	PidFile     string
	RunLog      string
	RPCPort     uint
	RPCHost     string
	RPCPassword string
	RPCAskPass  bool
	NArg        int
)

func init() {
	flag.BoolVar(&Debug, "debug", false, "Debug mode")
	flag.StringVar(&CpuProfile, "cpuprofile", "", "write cpu profile to file")
	flag.UintVar(&RPCPort, "p", 3390, "Server port")
	flag.StringVar(&RPCHost, "h", "localhost", "Server host")
	flag.StringVar(&RPCPassword, "P", "", "Server password")
	flag.BoolVar(&RPCAskPass, "a", false, "Ask for server password")
	flag.Parse()
	NArg = flag.NArg()

	daemon := flag.NewFlagSet("daemon", flag.ExitOnError)
	daemon.BoolVar(&Debug, "debug", false, "Debug mode")
	daemon.StringVar(&CpuProfile, "cpuprofile", "", "write cpu profile to file")
	daemon.StringVar(&ConfigFile, "config", "", "Path to the config file")
	daemon.BoolVar(&Monitor, "monitor", true, "Enable the background mirrors monitor")
	daemon.StringVar(&PidFile, "p", "", "Path to pid file")
	daemon.StringVar(&RunLog, "log", "", "File to output logs (default: stderr)")

	if len(os.Args) > 1 && os.Args[1] == "daemon" {
		Daemon = true
		daemon.Parse(os.Args[2:])
	}
}
