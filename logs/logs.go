// Copyright (c) 2014-2015 Ludovic Fauvet
// Licensed under the MIT license

package logs

import (
	"bytes"
	"fmt"
	. "github.com/etix/mirrorbits/config"
	"github.com/etix/mirrorbits/core"
	"github.com/etix/mirrorbits/mirrors"
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
	rlogger runtimeLogger
	dlogger downloadsLogger
)

type runtimeLogger struct {
	f *os.File
}

type downloadsLogger struct {
	sync.RWMutex
	l *stdlog.Logger
	f *os.File
}

// ReloadLogs will reopen the logs to allow rotations
func ReloadLogs() {
	ReloadRuntimeLogs()
	if core.Daemon {
		ReloadDownloadLogs()
	}
}

func ReloadRuntimeLogs() {
	if rlogger.f == os.Stderr && core.RunLog == "" {
		// Logger already set up and connected to the console.
		// Don't reload to avoid breaking journald.
		return
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

	if core.RunLog != "" {
		var err error
		rlogger.f, err = os.OpenFile(core.RunLog, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Cannot open log file for writing")
			rlogger.f = os.Stderr
		} else {
			logColor = false
		}
	}

	logBackend := logging.NewLogBackend(rlogger.f, "", 0)
	logBackend.Color = logColor

	logging.SetBackend(logBackend)

	if core.Debug {
		logging.SetFormatter(logging.MustStringFormatter("%{shortfile:-20s}%{time:2006/01/02 15:04:05.000 MST} %{message}"))
		logging.SetLevel(logging.DEBUG, "main")
	} else {
		logging.SetFormatter(logging.MustStringFormatter("%{time:2006/01/02 15:04:05.000 MST} %{message}"))
		logging.SetLevel(logging.INFO, "main")
	}
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
func LogDownload(typ string, statuscode int, p *mirrors.Results, err error) {
	dlogger.RLock()
	defer dlogger.RUnlock()

	if dlogger.l == nil {
		// Logs are disabled
		return
	}

	errstr := "<unknown>"
	if err != nil {
		errstr = err.Error()
	}

	if (statuscode == 302 || statuscode == 200) && p != nil && len(p.MirrorList) > 0 {
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
	} else if statuscode == 404 && p != nil {
		dlogger.l.Printf("%s 404 \"%s\" ip:%s", typ, p.FileInfo.Path, p.IP)
	} else if statuscode == 500 && p != nil {
		mirrorID := "unknown"
		if len(p.MirrorList) > 0 {
			mirrorID = p.MirrorList[0].ID
		}
		dlogger.l.Printf("%s 500 \"%s\" ip:%s mirror:%s error:%s", typ, p.FileInfo.Path, p.IP, mirrorID, errstr)
	} else {
		var path, ip string
		if p != nil {
			path = p.FileInfo.Path
			ip = p.IP
		}
		dlogger.l.Printf("%s %d \"%s\" ip:%s error:%s", typ, statuscode, path, ip, errstr)
	}
}
