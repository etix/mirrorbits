// Copyright (c) 2014-2017 Ludovic Fauvet
// Licensed under the MIT license

package daemon

import (
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	. "github.com/etix/mirrorbits/config"
	"github.com/etix/mirrorbits/database"
	"github.com/etix/mirrorbits/mirrors"
	"github.com/etix/mirrorbits/utils"
)

const (
	clusterAnnouncePrefix = "HELLO"
)

type cluster struct {
	redis *database.Redis

	nodeID        string
	nodes         []node
	nodeIndex     int
	nodeTotal     int
	nodesLock     sync.RWMutex
	mirrorsIndex  []string
	stop          chan bool
	wg            sync.WaitGroup
	running       bool
	StartStopLock sync.Mutex
	announceText  string
}

type node struct {
	ID           string
	LastAnnounce int64
}

type byNodeID []node

func (n byNodeID) Len() int           { return len(n) }
func (n byNodeID) Swap(i, j int)      { n[i], n[j] = n[j], n[i] }
func (n byNodeID) Less(i, j int) bool { return n[i].ID < n[j].ID }

// NewCluster creates a new instance of the cluster agent
func NewCluster(r *database.Redis) *cluster {
	c := &cluster{
		redis: r,
		nodes: make([]node, 0),
		stop:  make(chan bool),
	}

	hostname := utils.Hostname()
	if len(hostname) == 0 {
		hostname = "unknown"
	}
	c.nodeID = fmt.Sprintf("%s-%05d", hostname, rand.Intn(32000))
	c.announceText = clusterAnnouncePrefix + strconv.Itoa(GetConfig().RedisDB)
	return c
}

func (c *cluster) Start() {
	c.StartStopLock.Lock()
	defer c.StartStopLock.Unlock()

	if c.running == true {
		return
	}
	log.Debug("Cluster starting...")
	c.running = true
	c.wg.Add(1)
	c.stop = make(chan bool)
	go c.clusterLoop()
}

func (c *cluster) Stop() {
	c.StartStopLock.Lock()
	defer c.StartStopLock.Unlock()

	select {
	case _, _ = <-c.stop:
		return
	default:
		close(c.stop)
		c.wg.Wait()
		c.running = false
		log.Debug("Cluster stopped")
	}
}

func (c *cluster) clusterLoop() {
	clusterChan := make(chan string, 10)
	announceTicker := time.NewTicker(1 * time.Second)

	c.refreshNodeList(c.nodeID, c.nodeID)
	c.redis.Pubsub.SubscribeEvent(database.CLUSTER, clusterChan)

	for {
		select {
		case <-c.stop:
			c.wg.Done()
			return
		case <-announceTicker.C:
			c.announce()
		case data := <-clusterChan:
			if !strings.HasPrefix(data, c.announceText+" ") {
				// Garbage
				continue
			}
			c.refreshNodeList(data[len(c.announceText)+1:], c.nodeID)
		}
	}
}

func (c *cluster) announce() {
	r := c.redis.Get()
	database.Publish(r, database.CLUSTER, fmt.Sprintf("%s %s", c.announceText, c.nodeID))
	r.Close()
}

func (c *cluster) refreshNodeList(nodeID, self string) {
	found := false

	c.nodesLock.Lock()

	// Expire unreachable nodes
	for i := 0; i < len(c.nodes); i++ {
		if utils.ElapsedSec(c.nodes[i].LastAnnounce, 5) && c.nodes[i].ID != nodeID && c.nodes[i].ID != self {
			log.Noticef("<- Node %s left the cluster", c.nodes[i].ID)
			c.nodes = append(c.nodes[:i], c.nodes[i+1:]...)
			i--
		} else if c.nodes[i].ID == nodeID {
			found = true
			c.nodes[i].LastAnnounce = time.Now().UTC().Unix()
		}
	}

	// Join new node
	if !found {
		if nodeID != self {
			log.Noticef("-> Node %s joined the cluster", nodeID)
		}
		n := node{
			ID:           nodeID,
			LastAnnounce: time.Now().UTC().Unix(),
		}
		// TODO use binary search here
		// See https://golang.org/pkg/sort/#Search
		c.nodes = append(c.nodes, n)
		sort.Sort(byNodeID(c.nodes))
	}

	c.nodeTotal = len(c.nodes)

	// TODO use binary search here
	// See https://golang.org/pkg/sort/#Search
	for i, n := range c.nodes {
		if n.ID == self {
			c.nodeIndex = i
			break
		}
	}

	c.nodesLock.Unlock()
}

func (c *cluster) AddMirror(mirror *mirrors.Mirror) {
	c.nodesLock.Lock()
	c.mirrorsIndex = addMirrorIDToSlice(c.mirrorsIndex, mirror.ID)
	c.nodesLock.Unlock()
}

func (c *cluster) RemoveMirror(mirror *mirrors.Mirror) {
	c.nodesLock.Lock()
	c.mirrorsIndex = removeMirrorIDFromSlice(c.mirrorsIndex, mirror.ID)
	c.nodesLock.Unlock()
}

func (c *cluster) IsHandled(mirrorID string) bool {
	c.nodesLock.RLock()
	index := sort.SearchStrings(c.mirrorsIndex, mirrorID)

	mRange := int(float32(len(c.mirrorsIndex))/float32(c.nodeTotal) + 0.5)
	start := mRange * c.nodeIndex
	c.nodesLock.RUnlock()

	// Check bounding to see if this mirror must be handled by this node.
	// The distribution of the nodes should be balanced except for the last node
	// that could contain one more node.
	if index >= start && (index < start+mRange || c.nodeIndex == c.nodeTotal-1) {
		return true
	}
	return false
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
