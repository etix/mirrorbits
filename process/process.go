// Copyright (c) 2014-2019 Ludovic Fauvet
// Licensed under the MIT license

package process

import (
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"path"
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
	// ErrInvalidfd is returned when the given file descriptor is invalid
	ErrInvalidfd = errors.New("invalid file descriptor")

	log = logging.MustGetLogger("main")
)

// Relaunch launches {self} as a child process passing listener details
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

	var file *os.File

	switch t := l.(type) {
	case *net.TCPListener:
		file, err = t.File()
	case *net.UnixListener:
		file, err = t.File()
	default:
		return ErrInvalidfd
	}
	if err != nil {
		return err
	}

	fd := file.Fd()
	sysfile := file.Name()

	listener, ok := l.(*net.TCPListener)
	if ok {
		listenerFile, err := listener.File()
		if err != nil {
			return err
		}
		fd = listenerFile.Fd()
		sysfile = listenerFile.Name()
	}

	if fd < uintptr(syscall.Stderr) {
		return ErrInvalidfd
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
	files[fd] = os.NewFile(fd, sysfile)
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
		err = fmt.Errorf("file descriptor is %T not *net.TCPListener or *net.UnixListener", i)
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

// KillParent sends a signal to make the parent exit gracefully with SIGQUIT
func KillParent(ppid int) error {
	log.Info("Asking parent to quit")
	return syscall.Kill(ppid, syscall.SIGQUIT)
}

// GetPidLocation finds the location to store our pid file
// and fallback to /var/run if none found
func GetPidLocation() string {
	if core.PidFile == "" { // Runtime
		rdir := os.Getenv("XDG_RUNTIME_DIR")
		if rdir == "" {
			if defaultPidFile == "" { // Compile time
				return "/run/mirrorbits/mirrorbits.pid" // Fallback
			}
			return defaultPidFile
		}
		return rdir + "/mirrorbits.pid"
	}
	return core.PidFile
}

// WritePidFile writes the current pid file to disk
func WritePidFile() {
	// Get the pid destination
	p := GetPidLocation()

	// Create the whole directory path
	if err := os.MkdirAll(path.Dir(p), 0755); err != nil {
		log.Errorf("Unable to write pid file: %v", err)
	}

	// Get our own PID and write it
	pid := strconv.Itoa(os.Getpid())
	if err := ioutil.WriteFile(p, []byte(pid), 0644); err != nil {
		log.Errorf("Unable to write pid file: %v", err)
	}
}

// RemovePidFile removes the current pid file
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

// GetRemoteProcPid gets the pid as it appears in the pid file (maybe not ours)
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
