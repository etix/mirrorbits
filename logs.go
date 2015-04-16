// Copyright (c) 2014 Ludovic Fauvet
// Licensed under the MIT license

package main

import (
	"bytes"
	"fmt"
	"github.com/op/go-logging"
	stdlog "log"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	log     = logging.MustGetLogger("main")
	rlogger RuntimeLogger
	dlogger DownloadsLogger
)

type RuntimeLogger struct {
	f *os.File
}

type DownloadsLogger struct {
	sync.RWMutex
	l *stdlog.Logger
	f *os.File
}

// ReloadLogs will reopen the logs to allow rotations
func ReloadLogs() {
	ReloadRuntimeLogs()
	ReloadDownloadLogs()
}

func ReloadRuntimeLogs() {
	logging.SetFormatter(logging.MustStringFormatter("%{time:2006/01/02 15:04:05.000 MST} %{message}"))
	logFlags := 0

	if debug {
		logFlags = stdlog.Lshortfile
		logging.SetLevel(logging.DEBUG, "main")
	} else {
		logging.SetLevel(logging.INFO, "main")
	}

	logColor := false

	stat, _ := os.Stdout.Stat()
	if (stat.Mode() & os.ModeCharDevice) != 0 {
		logColor = true //TODO make it optionnal
	}

	if rlogger.f != nil {
		rlogger.f.Close()
	} else {
		rlogger.f = os.Stderr
	}

	if runLog != "" {
		var err error
		rlogger.f, err = os.OpenFile(runLog, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Cannot open log file for writing")
			rlogger.f = os.Stderr
		} else {
			logColor = false
		}
	}

	logBackend := logging.NewLogBackend(rlogger.f, "", logFlags)
	logBackend.Color = logColor

	logging.SetBackend(logBackend)
}

func ReloadDownloadLogs() {
	dlogger.Lock()
	defer dlogger.Unlock()

	if GetConfig().LogDir == "" {
		if dlogger.f != nil {
			dlogger.f.Close()
		}
		dlogger.f = nil
		dlogger.l = nil
		return
	}

	logfile := GetConfig().LogDir + "/downloads.log"
	createHeader := true

	s, err := os.Stat(logfile)
	if err == nil && s.Size() > 0 {
		createHeader = false
	}

	if dlogger.f != nil {
		dlogger.f.Close()
	}
	dlogger.f, err = os.OpenFile(logfile, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0666)

	if err != nil {
		log.Critical("Warning: cannot open log file %s", logfile)
		return
	}

	if createHeader {
		var buf bytes.Buffer
		hostname, _ := os.Hostname()
		fmt.Fprintf(&buf, "# Log file created at: %s\n", time.Now().Format("2006/01/02 15:04:05"))
		fmt.Fprintf(&buf, "# Running on machine: %s\n", hostname)
		fmt.Fprintf(&buf, "# Binary: Built with %s %s for %s/%s\n", runtime.Compiler, runtime.Version(), runtime.GOOS, runtime.GOARCH)
		dlogger.f.Write(buf.Bytes())
	}

	dlogger.l = stdlog.New(dlogger.f, "", stdlog.Ldate|stdlog.Lmicroseconds)
}

// This function will write a download result in the logs.
func logDownload(typ string, statuscode int, p *MirrorlistPage, err error) {
	dlogger.RLock()
	defer dlogger.RUnlock()

	if dlogger.l == nil {
		// Logs are disabled
		return
	}

	if statuscode == 302 || statuscode == 200 {
		var distance, countries string
		m := p.MirrorList[0]
		distance = strconv.FormatFloat(float64(m.Distance), 'f', 2, 32)
		countries = strings.Join(m.CountryFields, ",")
		fallback := ""
		if p.Fallback == true {
			fallback = " fallback:true"
		}
		sameASNum := ""
		if m.Asnum > 0 && m.Asnum == p.ClientInfo.ASNum {
			sameASNum = "same"
		}

		dlogger.l.Printf("%s %d \"%s\" ip:%s mirror:%s%s %sasn:%d distance:%skm countries:%s",
			typ, statuscode, p.FileInfo.Path, p.IP, m.ID, fallback, sameASNum, m.Asnum, distance, countries)
	} else if statuscode == 404 {
		dlogger.l.Printf("%s 404 \"%s\" %s", typ, p.FileInfo.Path, p.IP)
	} else if statuscode == 500 {
		mirrorID := "unknown"
		if len(p.MirrorList) > 0 {
			mirrorID = p.MirrorList[0].ID
		}
		dlogger.l.Printf("%s 500 \"%s\" ip:%s mirror:%s error:%s", typ, p.FileInfo.Path, p.IP, mirrorID, err.Error())
	} else {
		dlogger.l.Printf("%s %d \"%s\" ip:%s error:%s", typ, statuscode, p.FileInfo.Path, p.IP, err.Error())
	}
}
