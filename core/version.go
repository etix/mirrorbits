// Copyright (c) 2014-2017 Ludovic Fauvet
// Licensed under the MIT license

package core

import (
	"fmt"
	"runtime"
)

var (
	VERSION = ""
	BUILD   = ""
	DEV     = ""
)

func PrintVersion() {
	fmt.Println("Version:", VERSION)
	fmt.Println("Build:", BUILD+DEV)
	fmt.Println("GoVersion:", runtime.Version())
	fmt.Println("Operating System:", runtime.GOOS)
	fmt.Println("Architecture:", runtime.GOARCH)
	fmt.Println("Gomaxprocs:", runtime.GOMAXPROCS(0))
}
