// Copyright (c) 2014-2020 Ludovic Fauvet
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

// VersionInfo is a struct containing version related informations
type VersionInfo struct {
	Version    string
	Build      string
	GoVersion  string
	OS         string
	Arch       string
	GoMaxProcs int
}

// GetVersionInfo returns the details of the current build
func GetVersionInfo() VersionInfo {
	return VersionInfo{
		Version:    VERSION,
		Build:      BUILD + DEV,
		GoVersion:  runtime.Version(),
		OS:         runtime.GOOS,
		Arch:       runtime.GOARCH,
		GoMaxProcs: runtime.GOMAXPROCS(0),
	}
}

// PrintVersion prints the versions contained in a VersionReply
func PrintVersion(info VersionInfo) {
	fmt.Printf(" %-17s %s\n", "Version:", info.Version)
	fmt.Printf(" %-17s %s\n", "Build:", info.Build)
	fmt.Printf(" %-17s %s\n", "GoVersion:", info.GoVersion)
	fmt.Printf(" %-17s %s\n", "Operating System:", info.OS)
	fmt.Printf(" %-17s %s\n", "Architecture:", info.Arch)
	fmt.Printf(" %-17s %d\n", "Gomaxprocs:", info.GoMaxProcs)
}
