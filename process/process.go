// Copyright (c) 2014-2017 Ludovic Fauvet
// Licensed under the MIT license

package process

import (
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"syscall"

	"github.com/etix/mirrorbits/core"
	"github.com/op/go-logging"
)

var (
	// Compile time variable
	defaultPidFile string
)

var (
	Invalidfd = errors.New("Invalid file descriptor")

	log = logging.MustGetLogger("main")
)

// Launch {self} as a child process passing listener details
// to provide a seamless binary upgrade.
func Relaunch(l net.Listener) error {
	argv0, err := exec.LookPath(os.Args[0])
	if err != nil {
		return err
	}
	if _, err := os.Stat(argv0); err != nil {
		return err
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	v := reflect.ValueOf(l).Elem().FieldByName("fd").Elem()
	fd := uintptr(v.FieldByName("sysfd").Int())

	if fd < uintptr(syscall.Stderr) {
		return Invalidfd
	}

	if err := os.Setenv("OLD_FD", fmt.Sprint(fd)); err != nil {
		return err
	}
	if err := os.Setenv("OLD_NAME", fmt.Sprintf("tcp:%s->", l.Addr().String())); err != nil {
		return err
	}
	if err := os.Setenv("OLD_PPID", fmt.Sprint(syscall.Getpid())); err != nil {
		return err
	}

	files := make([]*os.File, fd+1)
	files[syscall.Stdin] = os.Stdin
	files[syscall.Stdout] = os.Stdout
	files[syscall.Stderr] = os.Stderr
	files[fd] = os.NewFile(fd, string(v.FieldByName("sysfile").String()))
	p, err := os.StartProcess(argv0, os.Args, &os.ProcAttr{
		Dir:   wd,
		Env:   os.Environ(),
		Files: files,
		Sys:   &syscall.SysProcAttr{},
	})
	if err != nil {
		return err
	}
	log.Infof("Spawned child %d\n", p.Pid)
	return nil
}

// Recover from a seamless binary upgrade and use an already
// existing listener to take over the connections
func Recover() (l net.Listener, ppid int, err error) {
	var fd uintptr
	_, err = fmt.Sscan(os.Getenv("OLD_FD"), &fd)
	if err != nil {
		return
	}
	var i net.Listener
	i, err = net.FileListener(os.NewFile(fd, os.Getenv("OLD_NAME")))
	if err != nil {
		return
	}
	switch i.(type) {
	case *net.TCPListener:
		l = i.(*net.TCPListener)
	case *net.UnixListener:
		l = i.(*net.UnixListener)
	default:
		err = errors.New(fmt.Sprintf(
			"file descriptor is %T not *net.TCPListener or *net.UnixListener", i))
		return
	}
	if err = syscall.Close(int(fd)); err != nil {
		return
	}
	_, err = fmt.Sscan(os.Getenv("OLD_PPID"), &ppid)
	if err != nil {
		return
	}
	return
}

// Make the parent exit gracefully with SIGQUIT
func KillParent(ppid int) error {
	return syscall.Kill(ppid, syscall.SIGQUIT)
}

// Get the proper location to store our pid file
// and fallback to /var/run if none found
func GetPidLocation() string {
	if core.PidFile == "" { // Runtime
		if defaultPidFile == "" { // Compile time
			return "/var/run/mirrorbits.pid" // Fallback
		}
		return defaultPidFile
	}
	return core.PidFile
}

// Write the current pid file
func WritePidFile() {
	pid := fmt.Sprintf("%d", os.Getpid())
	if err := ioutil.WriteFile(GetPidLocation(), []byte(pid), 0644); err != nil {
		log.Errorf("Unable to write pid file: %v", err)
	}
}

// Remove the current pid file
func RemovePidFile() {
	pidFile := GetPidLocation()
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		// Ensures we don't remove our forked process pid file
		// This can happen during seamless binary upgrade
		if GetRemoteProcPid() == os.Getpid() {
			if err = os.Remove(pidFile); err != nil {
				log.Errorf("Unable to remove pid file: %v", err)
			}
		}
	}
}

// Get the pid as it appears in the pid file (maybe not ours)
func GetRemoteProcPid() int {
	b, err := ioutil.ReadFile(GetPidLocation())
	if err != nil {
		return -1
	}
	i, err := strconv.ParseInt(string(b), 10, 0)
	if err != nil {
		return -1
	}
	return int(i)
}
