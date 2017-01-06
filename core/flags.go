// Copyright (c) 2014-2017 Ludovic Fauvet
// Licensed under the MIT license

package core

import (
	"flag"
)

var (
	Daemon     bool
	Debug      bool
	Monitor    bool
	ConfigFile string
	CpuProfile string
	PidFile    string
	RunLog     string
)

func init() {
	flag.StringVar(&ConfigFile, "config", "", "Path to the config file")
	flag.BoolVar(&Daemon, "D", false, "Daemon mode")
	flag.BoolVar(&Debug, "debug", false, "Debug mode")
	flag.BoolVar(&Monitor, "monitor", true, "Enable the background mirrors monitor")
	flag.StringVar(&CpuProfile, "cpuprofile", "", "write cpu profile to file")
	flag.StringVar(&PidFile, "p", "", "Path to pid file")
	flag.StringVar(&RunLog, "log", "", "File to output logs (default: stderr)")
	flag.Parse()
}

func Args() []string {
	return flag.Args()
}
