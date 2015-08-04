// Copyright (c) 2015 wsnipex
// Licensed under the MIT license

package main

import (
	"github.com/rjeczalik/notify"
	"sync"
)

type EventMonitor struct {
	redis    *redisobj
	cache    *Cache
	mapLock  sync.Mutex
	syncChan chan string
	stop     chan bool
	wg       sync.WaitGroup
	buffer   chan notify.EventInfo
}

func NewEventMonitor(r *redisobj, c *Cache) *EventMonitor {
	fsmon := new(EventMonitor)
	fsmon.redis = r
	fsmon.cache = c
	fsmon.syncChan = make(chan string)
	fsmon.stop = make(chan bool)

	bufsize := GetConfig().EventBufferSize
	if bufsize < 1000 {
		bufsize = 1000
		log.Warning("EventBuffersize too low, adjusting to 1000")
	}
	fsmon.buffer = make(chan notify.EventInfo, bufsize)

	return fsmon
}

func (m *EventMonitor) Stop() {
	select {
	case _, _ = <-m.stop:
		return
	default:
		close(m.stop)
	}
}

func (m *EventMonitor) Wait() {
	m.wg.Wait()
}

func (m *EventMonitor) MonitorRepository() {
	var path string

	// set up a recursive watch on our repository
	path = GetConfig().Repository + "/..."
	if err := notify.Watch(path, m.buffer, notify.All); err != nil {
		log.Fatal(err)
	}
	defer notify.Stop(m.buffer)

	for {
		ei := <-m.buffer
		ScanFile(m.redis, ei.Path(), ei.Event().String())
	}
}
