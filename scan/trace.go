// Copyright (c) 2014-2017 Ludovic Fauvet
// Licensed under the MIT license

package scan

import (
    "bufio"
    "context"
    "errors"
    "fmt"
    . "github.com/etix/mirrorbits/config"
    "github.com/etix/mirrorbits/core"
    "github.com/etix/mirrorbits/database"
    "github.com/etix/mirrorbits/mirrors"
    "github.com/etix/mirrorbits/utils"
    "net"
    "net/http"
    "strconv"
    "time"
)

var (
    userAgent      = "Mirrorbits/" + core.VERSION + " TRACE"
    clientTimeout  = time.Duration(20 * time.Second)
    clientDeadline = time.Duration(40 * time.Second)

    ErrNoTrace = errors.New("No trace file")
)

type Trace struct {
    redis      *database.Redis
    transport  http.Transport
    httpClient http.Client
    stop       chan bool
}

func NewTraceHandler(redis *database.Redis, stop chan bool) *Trace {
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

func (t *Trace) GetLastUpdate(mirror mirrors.Mirror) error {
    traceFile := GetConfig().TraceFileLocation

    if len(traceFile) == 0 {
        return ErrNoTrace
    }

    log.Debugf("Getting latest trace file for %s...", mirror.ID)

    // Prepare the HTTP request
    req, err := http.NewRequest("GET", utils.ConcatURL(mirror.HttpURL, traceFile), nil)
    req.Header.Set("User-Agent", userAgent)
    req.Close = true

    // Prepare contexts
    ctx, cancel := context.WithTimeout(req.Context(), clientDeadline)
    ctx = context.WithValue(ctx, "mid", mirror.ID)
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

    _, err = conn.Do("HSET", fmt.Sprintf("MIRROR_%s", mirror.ID), "lastModTime", timestamp)
    if err != nil {
        return err
    }

    // Publish an update on redis
    database.Publish(conn, database.MIRROR_UPDATE, mirror.ID)

    log.Debugf("[%s] trace last sync: %s", mirror.ID, time.Unix(timestamp, 0))
    return nil
}
