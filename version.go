// Copyright (c) 2014 Ludovic Fauvet
// Licensed under the MIT license

package main

import (
	"fmt"
	"runtime"
)

var (
	VERSION = "0.3-dev"
)

func printVersion() {
	fmt.Println("Version:", VERSION)
	fmt.Println("GoVersion:", runtime.Version())
	fmt.Println("Operating System:", runtime.GOOS)
	fmt.Println("Architecture:", runtime.GOARCH)
	fmt.Println("Gomaxprocs:", runtime.GOMAXPROCS(0))
}
