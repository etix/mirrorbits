// Copyright (c) 2014-2015 Ludovic Fauvet
// Licensed under the MIT license

package scan

import (
	"bufio"
	"errors"
	"fmt"
	"github.com/etix/mirrorbits/utils"
	"github.com/garyburd/redigo/redis"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

var (
	rsyncOutputLine = regexp.MustCompile(`^.+\s+([0-9,]+)\s+([0-9/]+)+\s+([0-9:]+)\s+(.*)$`)
)

type RsyncScanner struct {
	scan *scan
}

func (r *RsyncScanner) Scan(url, identifier string, conn redis.Conn, stop chan bool) error {
	if !strings.HasPrefix(url, "rsync://") {
		return fmt.Errorf("%s does not start with rsync://", url)
	}

	// Always ensures there's a trailing slash
	if url[len(url)-1] != '/' {
		url = url + "/"
	}

	cmd := exec.Command("rsync", "-r", "--no-motd", "--timeout=30", "--contimeout=30", url)
	stdout, err := cmd.StdoutPipe()

	if err != nil {
		return err
	}

	// Pipe stdout
	reader := bufio.NewReader(stdout)

	if utils.IsStopped(stop) {
		return ScanAborted
	}

	// Start the process
	if err := cmd.Start(); err != nil {
		return err
	}

	log.Info("[%s] Requesting file list via rsync...", identifier)

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

	// Get the list of all source files (we do not want to
	// index files than are not provided by the source)
	//sourceFiles, err := redis.Values(conn.Do("SMEMBERS", "FILES"))
	//if err != nil {
	//  log.Error("[%s] Cannot get the list of source files", identifier)
	//  return err
	//}

	count := 0

	line, err := readln(reader)
	for err == nil {
		var size int64
		var f filedata

		if utils.IsStopped(stop) {
			return ScanAborted
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
			log.Error("[%s] ScanRsync: Invalid size: %s", identifier, ret[1])
			goto cont
		}

		// Fill the struct
		f.size = size
		f.path = ret[4]

		if os.Getenv("DEBUG") != "" {
			//fmt.Printf("[%s] %s", identifier, f.path)
		}

		r.scan.ScannerAddFile(f)

		count++
	cont:
		line, err = readln(reader)
	}

	if err1 := cmd.Wait(); err1 != nil {
		switch err1.Error() {
		case "exit status 5":
			err1 = errors.New("rsync: Error starting client-server protocol")
			break
		case "exit status 10":
			err1 = errors.New("rsync: Error in socket I/O")
			break
		case "exit status 11":
			err1 = errors.New("rsync: Error in file I/O")
			break
		case "exit status 30":
			err1 = errors.New("rsync: Timeout in data send/receive")
			break
		default:
			err1 = errors.New("rsync: " + err1.Error())
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
		isPrefix bool = true
		err      error
		line, ln []byte
	)
	for isPrefix && err == nil {
		line, isPrefix, err = r.ReadLine()
		ln = append(ln, line...)
	}
	return string(ln), err
}
