// Copyright (c) 2014-2015 Ludovic Fauvet
// Licensed under the MIT license

package main

import (
	"errors"
	"fmt"
	"github.com/garyburd/redigo/redis"
	"math/rand"
	"net"
	"net/http"
	"sort"
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
	mirrorNotScanned   = errors.New("Mirror has not yet been scanned")
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
	formatLongestID int

	nodes        []Node
	nodeIndex    int
	nodeTotal    int
	nodesLock    sync.RWMutex
	mirrorsIndex []string
}

type MMirror struct {
	Mirror
	checking  bool
	scanning  bool
	lastCheck int64
}

func NewMonitor(r *redisobj, c *Cache) *Monitor {
	monitor := new(Monitor)
	monitor.redis = r
	monitor.cache = c
	monitor.mirrors = make(map[string]*MMirror)
	monitor.healthCheckChan = make(chan string, healthCheckThreads*5)
	monitor.syncChan = make(chan string)
	monitor.stop = make(chan bool)
	monitor.nodes = make([]Node, 0)
	monitor.mirrorsIndex = make([]string, 0)

	rand.Seed(time.Now().UnixNano())

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
	m.redis.pubsub.SubscribeEvent(MIRROR_UPDATE, mirrorUpdateEvent)

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

	go m.clusterLoop()

	for i := 0; i < healthCheckThreads; i++ {
		go m.healthCheckLoop()
	}

	for i := 0; i < GetConfig().ConcurrentSync; i++ {
		go m.syncLoop()
	}

	// Setup recurrent tasks
	sourceSyncTicker := time.NewTicker(5 * time.Minute)
	mirrorCheckTicker := time.NewTicker(1 * time.Second)

	for {
		select {
		case <-m.stop:
			m.wg.Done()
			return
		case id := <-mirrorUpdateEvent:
			m.syncMirrorList(id)
		case <-sourceSyncTicker.C:
			m.syncSource()
		case <-mirrorCheckTicker.C:
			m.mapLock.Lock()
			for k, v := range m.mirrors {
				if !v.Enabled {
					// Ignore disabled mirrors
					continue
				}
				if elapsedSec(v.lastCheck, int64(60*GetConfig().CheckInterval)) && m.mirrors[k].checking == false && m.isHandled(k) {
					select {
					case m.healthCheckChan <- k:
						m.mirrors[k].checking = true
					default:
					}
				}
				if elapsedSec(v.LastSync, int64(60*GetConfig().ScanInterval)) && m.mirrors[k].scanning == false && m.isHandled(k) {
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

func removeMirrorIDFromSlice(slice []string, mirrorID string) []string {
	// See https://golang.org/pkg/sort/#SearchStrings
	idx := sort.SearchStrings(slice, mirrorID)
	if idx < len(slice) && slice[idx] == mirrorID {
		slice = append(slice[:idx], slice[idx+1:]...)
	}
	return slice
}

func addMirrorIDToSlice(slice []string, mirrorID string) []string {
	// See https://golang.org/pkg/sort/#SearchStrings
	idx := sort.SearchStrings(slice, mirrorID)
	if idx >= len(slice) || slice[idx] != mirrorID {
		slice = append(slice[:idx], append([]string{mirrorID}, slice[idx:]...)...)
	}
	return slice
}

// Sync the remote mirror struct with the local dataset
// TODO needs improvements
func (m *Monitor) syncMirrorList(mirrorsIDs ...string) ([]Mirror, error) {

	mirrors := make([]Mirror, 0, len(mirrorsIDs))

	for _, id := range mirrorsIDs {
		if len(id) > m.formatLongestID {
			m.formatLongestID = len(id)
		}
		mirror, err := m.cache.GetMirror(id)
		if err != nil && err != redis.ErrNil {
			log.Error("Fetching mirror %s failed: %s", id, err.Error())
			continue
		} else if err == redis.ErrNil {
			// Mirror has been deleted
			m.mapLock.Lock()
			delete(m.mirrors, id)
			m.nodesLock.Lock()
			m.mirrorsIndex = removeMirrorIDFromSlice(m.mirrorsIndex, mirror.ID)
			m.nodesLock.Unlock()
			m.mapLock.Unlock()
			continue
		}
		mirrors = append(mirrors, mirror)

		m.mapLock.Lock()
		m.nodesLock.Lock()
		m.mirrorsIndex = addMirrorIDToSlice(m.mirrorsIndex, mirror.ID)
		m.nodesLock.Unlock()
		m.mapLock.Unlock()
	}

	m.mapLock.Lock()

	// Prepare the list of mirrors
	for _, e := range mirrors {
		var isChecking bool = false
		var isScanning bool = false
		var lastCheck int64 = 0
		if _, ok := m.mirrors[e.ID]; ok {
			isChecking = m.mirrors[e.ID].checking
			isScanning = m.mirrors[e.ID].scanning
			lastCheck = m.mirrors[e.ID].lastCheck
		}
		m.mirrors[e.ID] = &MMirror{
			Mirror:    e,
			checking:  isChecking,
			scanning:  isScanning,
			lastCheck: lastCheck,
		}
	}
	m.mapLock.Unlock()

	log.Debug("%d mirror%s updated", len(mirrorsIDs), plural(len(mirrorsIDs)))
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

			if m.healthCheck(mirror.Mirror) == mirrorNotScanned {
				// Not removing the 'checking' lock is intended here so the mirror won't
				// be checked again until the rsync/ftp scan is finished.
				continue
			}

			m.mapLock.Lock()
			if _, ok := m.mirrors[k]; ok {
				m.mirrors[k].lastCheck = time.Now().UTC().Unix()
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

			conn := m.redis.pool.Get()
			scanning, err := IsScanning(conn, k)
			if err != nil {
				log.Error("syncloop: ", err.Error())
				conn.Close()
				goto unlock
			} else if scanning {
				// A scan is already in progress on another node
				conn.Close()
				goto unlock
			}
			conn.Close()

			log.Debug("Scanning %s", k)

			err = NoSyncMethod

			// First try to scan with rsync
			if mirror.RsyncURL != "" {
				err = Scan(RSYNC, m.redis, mirror.RsyncURL, k, m.stop)
			}
			// If it failed or rsync wasn't supported
			// fallback to FTP
			if err != nil && mirror.FtpURL != "" {
				err = Scan(FTP, m.redis, mirror.FtpURL, k, m.stop)
			}

			if err == scanInProgress {
				log.Warning("%-30.30s Scan already in progress", k)
				goto unlock
			}

			select {
			case m.healthCheckChan <- k:
			default:
			}

		unlock:
			m.mapLock.Lock()
			if _, ok := m.mirrors[k]; ok {
				m.mirrors[k].scanning = false
			}
			m.mapLock.Unlock()
		}
	}
}

// Do an actual health check against a given mirror
func (m *Monitor) healthCheck(mirror Mirror) error {
	format := "%-" + fmt.Sprintf("%d.%ds", m.formatLongestID+4, m.formatLongestID+4)

	file, size, err := m.getRandomFile(mirror.ID)
	if err != nil {
		if err == redis.ErrNil {
			return mirrorNotScanned
		} else {
			log.Warning(format+"Error: Cannot obtain a random file: %s", mirror.ID, err)
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
		log.Error(format+"Error: %s (%dms)", mirror.ID, err.Error(), elapsed/time.Millisecond)
		return err
	}
	resp.Body.Close()

	contentLength := resp.Header.Get("Content-Length")

	if resp.StatusCode == 404 {
		markMirrorDown(m.redis, mirror.ID, fmt.Sprintf("File not found %s (error 404)", file))
		if GetConfig().DisableOnMissingFile {
			disableMirror(m.redis, mirror.ID)
		}
		log.Error(format+"Error: File %s not found (error 404)", mirror.ID, file)
	} else if resp.StatusCode != 200 {
		markMirrorDown(m.redis, mirror.ID, fmt.Sprintf("Got status code %d", resp.StatusCode))
		log.Warning(format+"Down! Status: %d", mirror.ID, resp.StatusCode)
	} else {
		markMirrorUp(m.redis, mirror.ID)
		rsize, err := strconv.ParseInt(contentLength, 10, 64)
		if err == nil && rsize != size {
			log.Warning(format+"File size mismatch! [%s] (%dms)", mirror.ID, file, elapsed/time.Millisecond)
		} else {
			log.Notice(format+"Up! (%dms)", mirror.ID, elapsed/time.Millisecond)
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
	err := ScanSource(m.redis, m.stop)
	if err != nil {
		log.Error("Scanning source failed: %s", err.Error())
	}
}

type Node struct {
	ID           string
	LastAnnounce int64
}

type ByNodeID []Node

func (n ByNodeID) Len() int           { return len(n) }
func (n ByNodeID) Swap(i, j int)      { n[i], n[j] = n[j], n[i] }
func (n ByNodeID) Less(i, j int) bool { return n[i].ID < n[j].ID }

func (m *Monitor) clusterLoop() {
	clusterChan := make(chan string, 10)
	announceTicker := time.NewTicker(1 * time.Second)

	hostname := getHostname()
	nodeID := fmt.Sprintf("%s-%05d", hostname, rand.Intn(32000))

	m.refreshNodeList(nodeID, nodeID)
	m.redis.pubsub.SubscribeEvent(CLUSTER, clusterChan)

	m.wg.Add(1)
	for {
		select {
		case <-m.stop:
			m.wg.Done()
			return
		case <-announceTicker.C:
			r := m.redis.pool.Get()
			Publish(r, CLUSTER, fmt.Sprintf("HELLO %s", nodeID))
			r.Close()
		case data := <-clusterChan:
			if !strings.HasPrefix(data, "HELLO ") {
				// Garbarge
				continue
			}
			m.refreshNodeList(data[6:], nodeID)
		}
	}
}

func (m *Monitor) refreshNodeList(nodeID, self string) {
	found := false

	m.nodesLock.Lock()

	// Expire unreachable nodes
	for i := 0; i < len(m.nodes); i++ {
		if elapsedSec(m.nodes[i].LastAnnounce, 5) && m.nodes[i].ID != nodeID && m.nodes[i].ID != self {
			log.Notice("<- Node %s left the cluster", m.nodes[i].ID)
			m.nodes = append(m.nodes[:i], m.nodes[i+1:]...)
			i--
		} else if m.nodes[i].ID == nodeID {
			found = true
			m.nodes[i].LastAnnounce = time.Now().UTC().Unix()
		}
	}

	// Join new node
	if !found {
		if nodeID != self {
			log.Notice("-> Node %s joined the cluster", nodeID)
		}
		n := Node{
			ID:           nodeID,
			LastAnnounce: time.Now().UTC().Unix(),
		}
		// TODO use binary search here
		// See https://golang.org/pkg/sort/#Search
		m.nodes = append(m.nodes, n)
		sort.Sort(ByNodeID(m.nodes))
	}

	m.nodeTotal = len(m.nodes)

	// TODO use binary search here
	// See https://golang.org/pkg/sort/#Search
	for i, n := range m.nodes {
		if n.ID == self {
			m.nodeIndex = i
			break
		}
	}

	m.nodesLock.Unlock()
}

func (m *Monitor) isHandled(mirrorID string) bool {
	m.nodesLock.RLock()
	index := sort.SearchStrings(m.mirrorsIndex, mirrorID)

	mRange := int(float32(len(m.mirrorsIndex))/float32(m.nodeTotal) + 0.5)
	start := mRange * m.nodeIndex
	m.nodesLock.RUnlock()

	// Check bounding to see if this mirror must be handled by this node.
	// The distribution of the nodes should be balanced except for the last node
	// that could contain one more node.
	if index >= start && (index < start+mRange || m.nodeIndex == m.nodeTotal-1) {
		return true
	}
	return false
}
