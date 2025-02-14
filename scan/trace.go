// Copyright (c) 2014-2019 Ludovic Fauvet
// Licensed under the MIT license

package scan

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	. "github.com/etix/mirrorbits/config"
	"github.com/etix/mirrorbits/core"
	"github.com/etix/mirrorbits/database"
	"github.com/etix/mirrorbits/mirrors"
	"github.com/etix/mirrorbits/utils"
)

var (
	userAgent      = "Mirrorbits/" + core.VERSION + " TRACE"
	clientTimeout  = time.Duration(20 * time.Second)
	clientDeadline = time.Duration(40 * time.Second)

	// ErrNoTrace is returned when no trace file is found
	ErrNoTrace = errors.New("No trace file")
)

// Trace is the internal trace handler
type Trace struct {
	redis      *database.Redis
	transport  http.Transport
	httpClient http.Client
	stop       <-chan struct{}
}

// NewTraceHandler returns a new instance of the trace file handler.
// Trace files are used to compute the time offset between a mirror
// and the local repository.
func NewTraceHandler(redis *database.Redis, stop <-chan struct{}) *Trace {
	t := &Trace{
		redis: redis,
		stop:  stop,
	}

	t.transport = http.Transport{
		DisableKeepAlives:   true,
		MaxIdleConnsPerHost: 0,
		Dial: func(network, addr string) (net.Conn, error) {
			deadline := time.Now().Add(clientDeadline)
			c, err := net.DialTimeout(network, addr, clientTimeout)
			if err != nil {
				return nil, err
			}
			c.SetDeadline(deadline)
			return c, nil
		},
	}

	t.httpClient = http.Client{
		Transport: &t.transport,
	}

	return t
}

// GetLastUpdate connects in HTTP to the mirror to get the latest
// trace file and computes the offset of the mirror.
func (t *Trace) GetLastUpdate(mirror mirrors.Mirror) error {
	traceFile := GetConfig().TraceFileLocation

	if len(traceFile) == 0 {
		return ErrNoTrace
	}

	log.Debugf("Getting latest trace file for %s...", mirror.Name)

	// Prepare the mirror URL
	var mirrorURL string
	if utils.HasAnyPrefix(mirror.HttpURL, "http://", "https://") {
		mirrorURL = mirror.HttpURL
	} else if mirror.HttpsUp == true {
		mirrorURL = "https://" + mirror.HttpURL
	} else {
		mirrorURL = "http://" + mirror.HttpURL
	}

	// Prepare the HTTP request
	req, err := http.NewRequest("GET", utils.ConcatURL(mirrorURL, traceFile), nil)
	req.Header.Set("User-Agent", userAgent)
	req.Close = true

	// Prepare contexts
	ctx, cancel := context.WithTimeout(req.Context(), clientDeadline)
	ctx = context.WithValue(ctx, core.ContextMirrorID, mirror.ID)
	req = req.WithContext(ctx)
	defer cancel()

	go func() {
		select {
		case <-t.stop:
			cancel()
		case <-ctx.Done():
		}
	}()

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(bufio.NewReader(resp.Body))
	scanner.Split(bufio.ScanWords)
	scanner.Scan()
	if err := scanner.Err(); err != nil {
		return err
	}

	timestamp, err := strconv.ParseInt(scanner.Text(), 10, 64)
	if err != nil {
		return err
	}

	conn := t.redis.Get()
	defer conn.Close()

	_, err = conn.Do("HSET", fmt.Sprintf("MIRROR_%d", mirror.ID), "lastModTime", timestamp)
	if err != nil {
		return err
	}

	// Publish an update on redis
	database.Publish(conn, database.MIRROR_UPDATE, strconv.Itoa(mirror.ID))

	log.Debugf("[%s] trace last sync: %s", mirror.Name, time.Unix(timestamp, 0))
	return nil
}
