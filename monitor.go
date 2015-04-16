// Copyright (c) 2014-2015 Ludovic Fauvet
// Licensed under the MIT license

package main

import (
	"errors"
	"fmt"
	"github.com/garyburd/redigo/redis"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	healthCheckThreads = 10
	userAgent          = "Mirrorbits/" + VERSION + " PING CHECK"
	clientTimeout      = time.Duration(20 * time.Second)
	clientDeadline     = time.Duration(40 * time.Second)
	redirectError      = errors.New("Redirect not allowed")
)

type Monitor struct {
	redis           *redisobj
	cache           *Cache
	mirrors         map[string]*MMirror
	mapLock         sync.Mutex
	httpClient      http.Client
	healthCheckChan chan string
	syncChan        chan string
	stop            chan bool
	wg              sync.WaitGroup
}

type MMirror struct {
	Mirror
	checking   bool
	scanning   bool
	lastUpdate int64
	lastScan   int64 //FIXME this could be removed since the mirrors are updated after each events no?
}

func NewMonitor(r *redisobj, c *Cache) *Monitor {
	monitor := new(Monitor)
	monitor.redis = r
	monitor.cache = c
	monitor.mirrors = make(map[string]*MMirror)
	monitor.healthCheckChan = make(chan string, healthCheckThreads*5)
	monitor.syncChan = make(chan string)
	monitor.stop = make(chan bool)

	transport := http.Transport{
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

	monitor.httpClient = http.Client{
		CheckRedirect: checkRedirect,
		Transport:     &transport,
	}
	return monitor
}

func (m *Monitor) Terminate() {
	close(m.stop)
	m.wg.Wait()
}

// Return an error if the endpoint is an unauthorized redirect
func checkRedirect(req *http.Request, via []*http.Request) error {
	if GetConfig().DisallowRedirects {
		return redirectError
	}
	return nil
}

// Main monitor loop
func (m *Monitor) monitorLoop() {
	m.wg.Add(1)
	m.syncSource()

	mirrorUpdateEvent := make(chan string, 10)
	m.cache.SubscribeEvent(MIRROR_UPDATE, mirrorUpdateEvent)

	for {
		ids, err := m.mirrorsID()
		if err == nil {
			m.syncMirrorList(ids...)
			break
		}
		select {
		case <-m.stop:
			m.wg.Done()
			return
		case <-time.After(500 * time.Millisecond):
		}
	}

	for i := 0; i < healthCheckThreads; i++ {
		go m.healthCheckLoop()
	}

	for i := 0; i < GetConfig().ConcurrentSync; i++ {
		go m.syncLoop()
	}

	// Setup recurrent tasks
	sourceSyncTicker := time.NewTicker(5 * time.Minute)

	for {
		select {
		case <-m.stop:
			m.wg.Done()
			return
		case id := <-mirrorUpdateEvent:
			m.syncMirrorList(id)
		case <-sourceSyncTicker.C:
			m.syncSource()
		case <-time.After(1 * time.Second):
			m.mapLock.Lock()
			for k, v := range m.mirrors {
				if v.lastUpdate+60 < time.Now().UTC().Unix() &&
					m.mirrors[k].checking == false {
					select {
					case m.healthCheckChan <- k:
						m.mirrors[k].checking = true
					default:
					}
				}
				if v.lastScan+60*30 < time.Now().UTC().Unix() &&
					m.mirrors[k].scanning == false {
					select {
					case m.syncChan <- k:
						m.mirrors[k].scanning = true
					default:
					}
				}
			}
			m.mapLock.Unlock()
		}
	}
}

// Returns a list of all mirrors ID
func (m *Monitor) mirrorsID() ([]string, error) {
	rconn := m.redis.pool.Get()
	defer rconn.Close()

	return redis.Strings(rconn.Do("LRANGE", "MIRRORS", "0", "-1"))
}

// Sync the remote mirror struct with the local dataset
// TODO needs improvements
func (m *Monitor) syncMirrorList(mirrorsIDs ...string) ([]Mirror, error) {

	mirrors := make([]Mirror, 0, len(mirrorsIDs))

	for _, id := range mirrorsIDs {
		mirror, err := m.cache.GetMirror(id)
		if err != nil && err != redis.ErrNil {
			log.Error("Fetching mirror %s failed: %s", id, err.Error())
			continue
		} else if err == redis.ErrNil {
			// Mirror has been deleted
			m.mapLock.Lock()
			delete(m.mirrors, id)
			m.mapLock.Unlock()
			continue
		}
		if mirror.Enabled {
			mirrors = append(mirrors, mirror)
		}
	}

	m.mapLock.Lock()

	// Prepare the list of mirrors
	for _, e := range mirrors {
		var isChecking bool = false
		var isScanning bool = false
		var lastUpdate int64 = 0
		var lastScan int64 = e.LastSync
		if _, ok := m.mirrors[e.ID]; ok {
			isChecking = m.mirrors[e.ID].checking
			isScanning = m.mirrors[e.ID].scanning
			lastUpdate = m.mirrors[e.ID].lastUpdate
			if m.mirrors[e.ID].lastScan > e.LastSync {
				lastScan = m.mirrors[e.ID].lastScan
			}
		}
		m.mirrors[e.ID] = &MMirror{
			Mirror:     e,
			checking:   isChecking,
			scanning:   isScanning,
			lastUpdate: lastUpdate,
			lastScan:   lastScan,
		}
	}
	m.mapLock.Unlock()

	log.Debug("Mirror list updated")
	return mirrors, nil
}

// Main health check loop
// TODO merge with the monitorLoop?
func (m *Monitor) healthCheckLoop() {
	m.wg.Add(1)
	for {
		select {
		case <-m.stop:
			m.wg.Done()
			return
		case k := <-m.healthCheckChan:
			m.mapLock.Lock()
			mirror := m.mirrors[k]
			m.mapLock.Unlock()

			m.healthCheck(mirror.Mirror)

			m.mapLock.Lock()
			if _, ok := m.mirrors[k]; ok {
				m.mirrors[k].lastUpdate = time.Now().UTC().Unix()
				m.mirrors[k].checking = false
			}
			m.mapLock.Unlock()
		}
	}
}

// Main sync loop
// TODO merge with the monitorLoop?
func (m *Monitor) syncLoop() {
	m.wg.Add(1)
	for {
		select {
		case <-m.stop:
			m.wg.Done()
			return
		case k := <-m.syncChan:
			m.mapLock.Lock()
			mirror := m.mirrors[k]
			m.mapLock.Unlock()

			log.Debug("Scanning %s", k)

			var err error = NoSyncMethod

			// First try to scan with rsync
			if mirror.RsyncURL != "" {
				err = Scan(m.redis).ScanRsync(mirror.RsyncURL, k, m.stop)
			}
			// If it failed or rsync wasn't supported
			// fallback to FTP
			if err != nil && mirror.FtpURL != "" {
				err = Scan(m.redis).ScanFTP(mirror.FtpURL, k, m.stop)
			}

			if err != nil {
				log.Error("%-30.30s Scan failed: %s", k, err.Error())
			}

			select {
			case m.healthCheckChan <- k:
			default:
			}

			m.mapLock.Lock()
			if _, ok := m.mirrors[k]; ok {
				m.mirrors[k].lastScan = time.Now().UTC().Unix()
				m.mirrors[k].scanning = false
			}
			m.mapLock.Unlock()
		}
	}
}

// Do an actual health check against a given mirror
func (m *Monitor) healthCheck(mirror Mirror) error {
	file, size, err := m.getRandomFile(mirror.ID)
	if err != nil {
		if err != redis.ErrNil {
			log.Warning("%-30.30s Error: Cannot obtain a random file: %s", mirror.ID, err)
		}
		return err
	}

	req, err := http.NewRequest("HEAD", strings.TrimRight(mirror.HttpURL, "/")+file, nil)
	req.Header.Set("User-Agent", userAgent)
	req.Close = true

	start := time.Now()
	resp, err := m.httpClient.Do(req)
	elapsed := time.Since(start)

	if err != nil {
		if opErr, ok := err.(*net.OpError); ok {
			log.Debug("Op: %s | Net: %s | Addr: %s | Err: %s | Temporary: %t", opErr.Op, opErr.Net, opErr.Addr, opErr.Error(), opErr.Temporary())
		}
		markMirrorDown(m.redis, mirror.ID, "Unreachable")
		log.Error("%-30.30s Error: %s (%dms)", mirror.ID, err.Error(), elapsed/time.Millisecond)
		return err
	}
	resp.Body.Close()

	contentLength := resp.Header.Get("Content-Length")

	if resp.StatusCode == 404 {
		markMirrorDown(m.redis, mirror.ID, fmt.Sprintf("File not found %s (error 404)", file))
		if GetConfig().DisableOnMissingFile {
			disableMirror(m.redis, mirror.ID)
		}
		log.Error("%-30.30s Error: File %s not found (error 404)", mirror.ID, file)
	} else if resp.StatusCode != 200 {
		markMirrorDown(m.redis, mirror.ID, fmt.Sprintf("Got status code %d", resp.StatusCode))
		log.Warning("%-30.30s Down! Status: %d", mirror.ID, resp.StatusCode)
	} else {
		markMirrorUp(m.redis, mirror.ID)
		rsize, err := strconv.ParseInt(contentLength, 10, 64)
		if err == nil && rsize != size {
			log.Warning("%-30.30s File size mismatch! [%s] (%dms)", mirror.ID, file, elapsed/time.Millisecond)
		} else {
			log.Notice("%-30.30s Up! (%dms)", mirror.ID, elapsed/time.Millisecond)
		}
	}
	return nil
}

// Get a random filename known to be served by the given mirror
func (m *Monitor) getRandomFile(identifier string) (file string, size int64, err error) {
	sinterKey := fmt.Sprintf("HANDLEDFILES_%s", identifier)

	rconn := m.redis.pool.Get()
	defer rconn.Close()

	file, err = redis.String(rconn.Do("SRANDMEMBER", sinterKey))
	if err != nil {
		return
	}

	size, err = redis.Int64(rconn.Do("HGET", fmt.Sprintf("FILE_%s", file), "size"))
	if err != nil {
		return
	}

	return
}

// Trigger a sync of the local repository
func (m *Monitor) syncSource() {
	err := Scan(m.redis).ScanSource(m.stop)
	if err != nil {
		log.Error("Scanning source failed: %s", err.Error())
	}
}
