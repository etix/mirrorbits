// Copyright (c) 2014-2015 Ludovic Fauvet
// Licensed under the MIT license

package main

import (
	"fmt"
	"github.com/garyburd/redigo/redis"
	"strconv"
	"strings"
	"time"
	"unsafe"
)

// Cache implements a local caching mechanism of type LRU for content available in the
// redis database that is automatically invalidated if the object is updated in Redis.
type Cache struct {
	r        *redisobj
	fiCache  *LRUCache
	fmCache  *LRUCache
	mCache   *LRUCache
	fimCache *LRUCache

	mirrorUpdateEvent      chan string
	fileUpdateEvent        chan string
	mirrorFileUpdateEvent  chan string
	pubsubReconnectedEvent chan string
}

type fileInfoValue struct {
	value FileInfo
}

func (f *fileInfoValue) Size() int {
	return int(unsafe.Sizeof(f.value))
}

type fileMirrorValue struct {
	value []string
}

func (f *fileMirrorValue) Size() int {
	return cap(f.value)
}

type mirrorValue struct {
	value Mirror
}

func (f *mirrorValue) Size() int {
	return int(unsafe.Sizeof(f.value))
}

// NewCache constructs a new instance of Cache
func NewCache(r *redisobj) *Cache {
	c := new(Cache)
	c.r = r
	c.fiCache = NewLRUCache(1024000)
	c.fmCache = NewLRUCache(2048000)
	c.mCache = NewLRUCache(1024000)
	c.fimCache = NewLRUCache(4096000)

	// Create event channels
	c.mirrorUpdateEvent = make(chan string, 10)
	c.fileUpdateEvent = make(chan string, 10)
	c.mirrorFileUpdateEvent = make(chan string, 10)
	c.pubsubReconnectedEvent = make(chan string)

	// Subscribe to events
	c.r.pubsub.SubscribeEvent(MIRROR_UPDATE, c.mirrorUpdateEvent)
	c.r.pubsub.SubscribeEvent(FILE_UPDATE, c.fileUpdateEvent)
	c.r.pubsub.SubscribeEvent(MIRROR_FILE_UPDATE, c.mirrorFileUpdateEvent)
	c.r.pubsub.SubscribeEvent(PUBSUB_RECONNECTED, c.pubsubReconnectedEvent)

	go func() {
		for {
			//FIXME add a close channel
			select {
			case data := <-c.mirrorUpdateEvent:
				c.mCache.Delete(data)
			case data := <-c.fileUpdateEvent:
				c.fiCache.Delete(data)
			case data := <-c.mirrorFileUpdateEvent:
				s := strings.SplitN(data, " ", 2)
				c.fmCache.Delete(s[1])
				c.fimCache.Delete(fmt.Sprintf("%s|%s", s[0], s[1]))
			case <-c.pubsubReconnectedEvent:
				c.Clear()
			}
		}
	}()

	return c
}

// Clear clears the local cache
func (c *Cache) Clear() {
	c.fiCache.Clear()
	c.fmCache.Clear()
	c.mCache.Clear()
	c.fimCache.Clear()
}

// GetFileInfo returns file information for a given file either from the cache
// or directly from the database if the object is not yet stored in the cache.
func (c *Cache) GetFileInfo(path string) (f FileInfo, err error) {
	v, ok := c.fiCache.Get(path)
	if ok {
		f = v.(*fileInfoValue).value
	} else {
		f, err = c.fetchFileInfo(path)
	}
	return
}

func (c *Cache) fetchFileInfo(path string) (f FileInfo, err error) {
	rconn := c.r.pool.Get()
	defer rconn.Close()
	reply, err := redis.Strings(rconn.Do("HMGET", fmt.Sprintf("FILE_%s", path), "size", "modTime", "sha1"))
	if err != nil {
		// Put at least the path in the response
		f.Path = path
		return
	}
	f.Path = path
	f.Size, _ = strconv.ParseInt(reply[0], 10, 64)
	f.ModTime, _ = time.Parse("2006-01-02 15:04:05.999999999 -0700 MST", reply[1])
	f.Sha1 = reply[2]
	c.fiCache.Set(path, &fileInfoValue{value: f})
	return
}

// GetMirrors returns all the mirrors serving a given file either from the cache
// or directly from the database if the object is not yet stored in the cache.
func (c *Cache) GetMirrors(path string, clientInfo GeoIPRec) (mirrors []Mirror, err error) {
	var mirrorsIDs []string
	v, ok := c.fmCache.Get(path)
	if ok {
		mirrorsIDs = v.(*fileMirrorValue).value
	} else {
		mirrorsIDs, err = c.fetchFileMirrors(path)
		if err != nil {
			return
		}
	}
	mirrors = make([]Mirror, 0, len(mirrorsIDs))
	for _, id := range mirrorsIDs {
		var mirror Mirror
		var fileInfo FileInfo
		v, ok := c.mCache.Get(id)
		if ok {
			mirror = v.(*mirrorValue).value
		} else {
			//TODO execute missing items in a MULTI query
			mirror, err = c.fetchMirror(id)
			if err != nil {
				return
			}
		}
		v, ok = c.fimCache.Get(fmt.Sprintf("%s|%s", id, path))
		if ok {
			fileInfo = v.(*fileInfoValue).value
		} else {
			fileInfo, err = c.fetchFileInfoMirror(id, path)
			if err != nil {
				return
			}
		}
		if fileInfo.Size >= 0 {
			mirror.FileInfo = &fileInfo
		}
		if clientInfo.GeoIPRecord != nil {
			mirror.Distance = getDistanceKm(clientInfo.Latitude,
				clientInfo.Longitude,
				mirror.Latitude,
				mirror.Longitude)
		} else {
			mirror.Distance = 0
		}
		mirrors = append(mirrors, mirror)
	}
	return
}

func (c *Cache) fetchFileMirrors(path string) (ids []string, err error) {
	rconn := c.r.pool.Get()
	defer rconn.Close()
	ids, err = redis.Strings(rconn.Do("SMEMBERS", fmt.Sprintf("FILEMIRRORS_%s", path)))
	if err != nil {
		return
	}
	c.fmCache.Set(path, &fileMirrorValue{value: ids})
	return
}

func (c *Cache) fetchMirror(mirrorID string) (mirror Mirror, err error) {
	rconn := c.r.pool.Get()
	defer rconn.Close()
	reply, err := redis.Values(rconn.Do("HGETALL", fmt.Sprintf("MIRROR_%s", mirrorID)))
	if err != nil {
		return
	}
	if len(reply) == 0 {
		err = redis.ErrNil
		return
	}
	err = redis.ScanStruct(reply, &mirror)
	if err != nil {
		return
	}
	mirror.CountryFields = strings.Fields(mirror.CountryCodes)
	c.mCache.Set(mirrorID, &mirrorValue{value: mirror})
	return
}

func (c *Cache) fetchFileInfoMirror(id, path string) (fileInfo FileInfo, err error) {
	rconn := c.r.pool.Get()
	defer rconn.Close()
	fileInfo.Size = -1
	reply, err := redis.Values(rconn.Do("HGETALL", fmt.Sprintf("FILEINFO_%s_%s", id, path)))
	if err != nil {
		return
	}
	err = redis.ScanStruct(reply, &fileInfo)
	if err != nil {
		return
	}
	c.fimCache.Set(fmt.Sprintf("%s|%s", id, path), &fileInfoValue{value: fileInfo})
	return
}

// GetMirror returns all information about a given mirror either from the cache
// or directly from the database if the object is not yet stored in the cache.
func (c *Cache) GetMirror(identifier string) (mirror Mirror, err error) {
	v, ok := c.mCache.Get(identifier)
	if ok {
		mirror = v.(*mirrorValue).value
	} else {
		mirror, err = c.fetchMirror(identifier)
		if err != nil {
			return
		}
	}
	return
}
