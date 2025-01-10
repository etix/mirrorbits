// Copyright (c) 2014-2019 Ludovic Fauvet
// Licensed under the MIT license

package mirrors

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/etix/mirrorbits/core"
	"github.com/etix/mirrorbits/database"
	"github.com/gomodule/redigo/redis"
	"github.com/op/go-logging"
)

var (
	log = logging.MustGetLogger("main")
)

type LogType uint

const (
	_ LogType = iota
	LOGTYPE_ERROR
	LOGTYPE_ADDED
	LOGTYPE_EDITED
	LOGTYPE_ENABLED
	LOGTYPE_DISABLED
	LOGTYPE_STATECHANGED
	LOGTYPE_SCANSTARTED
	LOGTYPE_SCANCOMPLETED
)

func typeToInstance(typ LogType) LogAction {
	switch LogType(typ) {
	case LOGTYPE_ERROR:
		return &LogError{}
	case LOGTYPE_ADDED:
		return &LogAdded{}
	case LOGTYPE_EDITED:
		return &LogEdited{}
	case LOGTYPE_ENABLED:
		return &LogEnabled{}
	case LOGTYPE_DISABLED:
		return &LogDisabled{}
	case LOGTYPE_STATECHANGED:
		return &LogStateChanged{}
	case LOGTYPE_SCANSTARTED:
		return &LogScanStarted{}
	case LOGTYPE_SCANCOMPLETED:
		return &LogScanCompleted{}
	default:
	}
	return nil
}

type LogAction interface {
	GetType() LogType
	GetMirrorID() int
	GetTimestamp() time.Time
	GetOutput() string
}

type LogCommonAction struct {
	Type      LogType
	MirrorID  int
	Timestamp time.Time
}

func (l LogCommonAction) GetType() LogType {
	return l.Type
}

func (l LogCommonAction) GetMirrorID() int {
	return l.MirrorID
}

func (l LogCommonAction) GetTimestamp() time.Time {
	return l.Timestamp
}

type LogError struct {
	LogCommonAction
	Err string
}

func (l *LogError) GetOutput() string {
	return fmt.Sprintf("Error: %s", l.Err)
}

func NewLogError(id int, err error) LogAction {
	return &LogError{
		LogCommonAction: LogCommonAction{
			Type:      LOGTYPE_ERROR,
			MirrorID:  id,
			Timestamp: time.Now(),
		},
		Err: err.Error(),
	}
}

type LogAdded struct {
	LogCommonAction
}

func (l *LogAdded) GetOutput() string {
	return "Mirror added"
}

func NewLogAdded(id int) LogAction {
	return &LogAdded{
		LogCommonAction: LogCommonAction{
			Type:      LOGTYPE_ADDED,
			MirrorID:  id,
			Timestamp: time.Now(),
		},
	}
}

type LogEdited struct {
	LogCommonAction
}

func (l *LogEdited) GetOutput() string {
	return "Mirror edited"
}

func NewLogEdited(id int) LogAction {
	return &LogEdited{
		LogCommonAction: LogCommonAction{
			Type:      LOGTYPE_EDITED,
			MirrorID:  id,
			Timestamp: time.Now(),
		},
	}
}

type LogEnabled struct {
	LogCommonAction
}

func (l *LogEnabled) GetOutput() string {
	return "Mirror enabled"
}

func NewLogEnabled(id int) LogAction {
	return &LogEnabled{
		LogCommonAction: LogCommonAction{
			Type:      LOGTYPE_ENABLED,
			MirrorID:  id,
			Timestamp: time.Now(),
		},
	}
}

type LogDisabled struct {
	LogCommonAction
}

func (l *LogDisabled) GetOutput() string {
	return "Mirror disabled"
}

func NewLogDisabled(id int) LogAction {
	return &LogDisabled{
		LogCommonAction: LogCommonAction{
			Type:      LOGTYPE_DISABLED,
			MirrorID:  id,
			Timestamp: time.Now(),
		},
	}
}

type LogStateChanged struct {
	LogCommonAction
	Proto  Protocol
	Up     bool
	Reason string
}

func (l *LogStateChanged) GetOutput() string {
	var mirror string

	switch l.Proto {
	case HTTP:
		mirror = "HTTP mirror"
	case HTTPS:
		mirror = "HTTPS mirror"
	default:
		mirror = "Mirror"
	}

	if l.Up == false {
		if len(l.Reason) == 0 {
			return mirror + " is down"
		}
		return mirror + " is down: " + l.Reason
	}

	return mirror + " is up"
}

func NewLogStateChanged(id int, proto Protocol, up bool, reason string) LogAction {
	return &LogStateChanged{
		LogCommonAction: LogCommonAction{
			Type:      LOGTYPE_STATECHANGED,
			MirrorID:  id,
			Timestamp: time.Now(),
		},
		Proto:  proto,
		Up:     up,
		Reason: reason,
	}
}

type LogScanStarted struct {
	LogCommonAction
	Typ core.ScannerType
}

func (l *LogScanStarted) GetOutput() string {
	switch l.Typ {
	case core.RSYNC:
		return "RSYNC scan started"
	case core.FTP:
		return "FTP scan started"
	default:
		return "Scan started using a unknown protocol"
	}
}

func NewLogScanStarted(id int, typ core.ScannerType) LogAction {
	return &LogScanStarted{
		LogCommonAction: LogCommonAction{
			Type:      LOGTYPE_SCANSTARTED,
			MirrorID:  id,
			Timestamp: time.Now(),
		},
		Typ: typ,
	}
}

type LogScanCompleted struct {
	LogCommonAction
	FilesIndexed int64
	KnownIndexed int64
	Removed      int64
	TZOffset     int64
}

func (l *LogScanCompleted) GetOutput() string {
	output := fmt.Sprintf("Scan completed: %d files (%d known), %d removed", l.FilesIndexed, l.KnownIndexed, l.Removed)
	if l.TZOffset != 0 {
		offset, _ := time.ParseDuration(fmt.Sprintf("%dms", l.TZOffset))
		output += fmt.Sprintf(" (corrected timezone offset: %s)", offset)
	}
	return output
}

func NewLogScanCompleted(id int, files, known, removed, tzoffset int64) LogAction {
	return &LogScanCompleted{
		LogCommonAction: LogCommonAction{
			Type:      LOGTYPE_SCANCOMPLETED,
			MirrorID:  id,
			Timestamp: time.Now(),
		},
		FilesIndexed: files,
		KnownIndexed: known,
		Removed:      removed,
		TZOffset:     tzoffset,
	}
}

func PushLog(r *database.Redis, logAction LogAction) error {
	conn := r.Get()
	defer conn.Close()

	key := fmt.Sprintf("MIRRORLOGS_%d", logAction.GetMirrorID())
	value, err := json.Marshal(logAction)
	if err != nil {
		return err
	}

	_, err = conn.Do("RPUSH", key, value)
	return err
}

func ReadLogs(r *database.Redis, mirrorid, max int) ([]string, error) {
	conn := r.Get()
	defer conn.Close()

	if max <= 0 {
		// Get the latest 500 events by default
		max = 500
	}

	key := fmt.Sprintf("MIRRORLOGS_%d", mirrorid)
	lines, err := redis.Strings(conn.Do("LRANGE", key, max*-1, -1))
	if err != nil {
		return nil, err
	}

	outputs := make([]string, 0, len(lines))

	for _, line := range lines {
		var objmap map[string]interface{}
		err = json.Unmarshal([]byte(line), &objmap)
		if err != nil {
			log.Warningf("Unable to parse mirror log line: %s", err)
			continue
		}

		typf, ok := objmap["Type"].(float64)
		if !ok {
			log.Warning("Unable to parse mirror log line")
			continue
		}

		// Truncate the received float64 back to int
		typ := int(typf)

		action := typeToInstance(LogType(typ))
		if action == nil {
			log.Warning("Unknown mirror log action")
			continue
		}

		err = json.Unmarshal([]byte(line), action)
		if err != nil {
			log.Warningf("Unable to unmarshal mirror log line: %s", err)
			continue
		}

		line := fmt.Sprintf("%s: %s", action.GetTimestamp().Format("2006-01-02 15:04:05 MST"), action.GetOutput())
		outputs = append(outputs, line)
	}

	return outputs, nil
}
