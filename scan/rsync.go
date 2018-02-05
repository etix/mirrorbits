// Copyright (c) 2014-2017 Ludovic Fauvet
// Licensed under the MIT license

package scan

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/etix/mirrorbits/utils"
	"github.com/garyburd/redigo/redis"
)

var (
	rsyncOutputLine = regexp.MustCompile(`^.+\s+([0-9,]+)\s+([0-9/]+)\s+([0-9:]+)\s+(.*)$`)
)

// RsyncScanner is the implementation of an rsync scanner
type RsyncScanner struct {
	scan *scan
}

// Scan starts an rsync scan of the given mirror
func (r *RsyncScanner) Scan(rsyncURL, identifier string, conn redis.Conn, stop chan bool) error {
	var env []string

	if !strings.HasPrefix(rsyncURL, "rsync://") {
		return fmt.Errorf("%s does not start with rsync://", rsyncURL)
	}

	u, err := url.Parse(rsyncURL)
	if err != nil {
		return err
	}

	// Extract the credentials
	if u.User != nil {
		if u.User.Username() != "" {
			env = append(env, fmt.Sprintf("USER=%s", u.User.Username()))
		}
		if password, ok := u.User.Password(); ok {
			env = append(env, fmt.Sprintf("RSYNC_PASSWORD=%s", password))
		}

		// Remove the credentials from the URL as we pass them through the environnement
		u.User = nil
	}

	// Don't use the local timezone, use UTC
	env = append(env, "TZ=UTC")

	cmd := exec.Command("rsync", "-r", "--no-motd", "--timeout=30", "--contimeout=30", "--exclude=.~tmp~/", u.String())

	// Setup the environnement
	cmd.Env = env

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	
	// Pipe stdout
	reader := bufio.NewReader(stdout)
	readerErr := bufio.NewReader(stderr)

	if utils.IsStopped(stop) {
		return ErrScanAborted
	}

	// Start the process
	if err := cmd.Start(); err != nil {
		return err
	}

	log.Infof("[%s] Requesting file list via rsync...", identifier)

	scanfinished := make(chan bool)
	go func() {
		select {
		case <-stop:
			cmd.Process.Kill()
			return
		case <-scanfinished:
			return
		}
	}()
	defer close(scanfinished)

	line, err := readln(reader)
	for err == nil {
		var size int64
		var f filedata

		if utils.IsStopped(stop) {
			return ErrScanAborted
		}

		// Parse one line returned by rsync
		ret := rsyncOutputLine.FindStringSubmatch(line)
		if ret[0][0] == 'd' || ret[0][0] == 'l' {
			// Skip directories and links
			goto cont
		}

		// Add the leading slash
		if ret[4][0] != '/' {
			ret[4] = "/" + ret[4]
		}

		// Remove the commas in the file size
		ret[1] = strings.Replace(ret[1], ",", "", -1)
		// Convert the size to int
		size, err = strconv.ParseInt(ret[1], 10, 64)
		if err != nil {
			log.Errorf("[%s] ScanRsync: Invalid size: %s", identifier, ret[1])
			goto cont
		}

		// Fill the struct
		f.size = size
		f.path = ret[4]

		r.scan.ScannerAddFile(f)

	cont:
		line, err = readln(reader)
	}

	rsyncErrors := []string{}
	for line, err = readln(readerErr); err == nil; line, err = readln(readerErr) {
		if strings.Contains(line, ": opendir ") {
			rsyncErrors = append(rsyncErrors, line)
		}
	}


	if err1 := cmd.Wait(); err1 != nil {
		switch err1.Error() {
		case "exit status 5":
			err1 = errors.New("rsync: Error starting client-server protocol")
		case "exit status 10":
			err1 = errors.New("rsync: Error in socket I/O")
		case "exit status 11":
			err1 = errors.New("rsync: Error in file I/O")
		case "exit status 23":
			for _, line := range rsyncErrors {
				log.Warningf("[%s] %s", identifier, line)
			}
			log.Warningf("[%s] rsync: Partial transfer due to error", identifier)
			err1 = nil
		case "exit status 30":
			err1 = errors.New("rsync: Timeout in data send/receive")
		case "exit status 35":
			err1 = errors.New("Timeout waiting for daemon connection")
		default:
			if utils.IsStopped(stop) {
				err1 = ErrScanAborted
			} else {
				err1 = errors.New("rsync: " + err1.Error())
			}
		}
		return err1
	}

	if err != io.EOF {
		return err
	}

	return nil
}

func readln(r *bufio.Reader) (string, error) {
	var (
		isPrefix = true
		err      error
		line, ln []byte
	)
	for isPrefix && err == nil {
		line, isPrefix, err = r.ReadLine()
		ln = append(ln, line...)
	}
	return string(ln), err
}
