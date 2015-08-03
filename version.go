// Copyright (c) 2014-2015 Ludovic Fauvet
// Licensed under the MIT license

package main

import (
	"fmt"
	"runtime"
)

var (
	VERSION = ""
	BUILD   = ""
	DEV     = ""
)

func printVersion() {
	fmt.Println("Version:", VERSION)
	fmt.Println("Build:", BUILD+DEV)
	fmt.Println("GoVersion:", runtime.Version())
	fmt.Println("Operating System:", runtime.GOOS)
	fmt.Println("Architecture:", runtime.GOARCH)
	fmt.Println("Gomaxprocs:", runtime.GOMAXPROCS(0))
}
