// Copyright (c) 2014 Ludovic Fauvet
// Licensed under the MIT license

package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

/*
	Total (all files, all mirrors):
	STATS_TOTAL

	List of hashes for a file:
	STATS_FILE							= path -> value		All time
	STATS_FILE_[year]					= path -> value		By year
	STATS_FILE_[year]_[month]			= path -> value		By month
	STATS_FILE_[year]_[month]_[day]		= path -> value		By day

	List of hashes for a mirror:
	STATS_MIRROR						= mirror -> value	All time
	STATS_MIRROR_[year]					= mirror -> value	By year
	STATS_MIRROR_[year]_[month]			= mirror -> value	By month
	STATS_MIRROR_[year]_[month]_[day]	= mirror -> value	By day
*/

var (
	emptyFileError = errors.New("stats: file parameter is empty")
	unknownMirror  = errors.New("stats: unknown mirror")
)

type Stats struct {
	r         *redisobj
	countChan chan CountItem
	mapStats  map[string]int64
	stop      chan bool
	wg        sync.WaitGroup
}

type CountItem struct {
	mirrorID string
	filepath string
	size     int64
	time     time.Time
}

func NewStats(redis *redisobj) *Stats {
	s := &Stats{
		r:         redis,
		countChan: make(chan CountItem, 1000),
		mapStats:  make(map[string]int64),
		stop:      make(chan bool),
	}
	go s.processCountDownload()
	return s
}

func (s *Stats) Terminate() {
	close(s.stop)
	log.Notice("Saving stats")
	s.wg.Wait()
}

func (s *Stats) CountDownload(m Mirror, fileinfo FileInfo) error {
	if m.ID == "" {
		return unknownMirror
	}
	if fileinfo.Path == "" {
		return emptyFileError
	}

	s.countChan <- CountItem{m.ID, fileinfo.Path, fileinfo.Size, time.Now()}
	return nil
}

func (s *Stats) processCountDownload() {
	s.wg.Add(1)
	pushTicker := time.NewTicker(500 * time.Millisecond)

	for {
		select {
		case <-s.stop:
			s.pushStats()
			s.wg.Done()
			return
		case c := <-s.countChan:
			date := c.time.Format("2006_01_02|") // Includes separator
			s.mapStats["f"+date+c.filepath] += 1
			s.mapStats["m"+date+c.mirrorID] += 1
			s.mapStats["s"+date+c.mirrorID] += c.size
		case <-pushTicker.C:
			s.pushStats()
		}
	}
}

func (s *Stats) pushStats() {
	if len(s.mapStats) <= 0 {
		return
	}

	rconn := s.r.pool.Get()
	defer rconn.Close()
	rconn.Send("MULTI")

	for k, v := range s.mapStats {
		if v == 0 {
			continue
		}

		separator := strings.Index(k, "|")
		if separator <= 0 {
			log.Critical("Stats: separator not found")
			continue
		}
		typ := k[:1]
		date := k[1:separator]
		object := k[separator+1:]

		if typ == "f" {
			fkey := fmt.Sprintf("STATS_FILE_%s", date)

			for i := 0; i < 4; i++ {
				rconn.Send("HINCRBY", fkey, object, v)
				fkey = fkey[:strings.LastIndex(fkey, "_")]
			}

			// Increase the total too
			rconn.Send("INCRBY", "STATS_TOTAL", v)
		} else if typ == "m" {
			mkey := fmt.Sprintf("STATS_MIRROR_%s", date)

			for i := 0; i < 4; i++ {
				rconn.Send("HINCRBY", mkey, object, v)
				mkey = mkey[:strings.LastIndex(mkey, "_")]
			}
		} else if typ == "s" {
			mkey := fmt.Sprintf("STATS_MIRROR_BYTES_%s", date)

			for i := 0; i < 4; i++ {
				rconn.Send("HINCRBY", mkey, object, v)
				mkey = mkey[:strings.LastIndex(mkey, "_")]
			}
		} else {
			log.Warning("Stats: unknown type", typ)
		}
	}

	_, err := rconn.Do("EXEC")

	if err != nil {
		fmt.Println("Stats: could not save stats to redis:", err)
		return
	}

	// Clear the map
	s.mapStats = make(map[string]int64)
}
