// Copyright (c) 2014-2017 Ludovic Fauvet
// Licensed under the MIT license

package network

import (
	"errors"
	"os"
	"time"

	"github.com/etix/mirrorbits/database"
	"github.com/garyburd/redigo/redis"
)

const (
	lockTTL     = 10 // in seconds
	lockRefresh = 5  // in seconds
)

// ClusterLock holds the internal structure of a ClusterLock
type ClusterLock struct {
	redis      *database.Redis
	key        string
	identifier string
	done       chan struct{}
}

// NewClusterLock returns a new instance of a ClusterLock.
// A ClucterLock is used to maitain a lock on a mirror that is being
// scanned. The lock is renewed every lockRefresh seconds and is
// automatically released by the redis database every lockTTL seconds
// allowing the lock to be released even if the application is killed.
func NewClusterLock(redis *database.Redis, key, identifier string) *ClusterLock {
	return &ClusterLock{
		redis:      redis,
		key:        key,
		identifier: identifier,
	}
}

// Get tries to obtain an exclusive lock, cluster wide, for the given mirror
func (n *ClusterLock) Get() (<-chan struct{}, error) {
	if n.done != nil {
		return nil, errors.New("lock already in use")
	}

	conn := n.redis.Get()
	defer conn.Close()

	if conn.Err() != nil {
		return nil, conn.Err()
	}

	_, err := redis.String(conn.Do("SET", n.key, 1, "NX", "EX", lockTTL))
	if err == redis.ErrNil {
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	n.done = make(chan struct{})

	// Maintain the lock active until release
	go func() {
		conn := n.redis.Get()
		defer conn.Close()

		for {
			select {
			case <-n.done:
				n.done = nil
				conn.Do("DEL", n.key)
				return
			case <-time.After(lockRefresh * time.Second):
				result, err := redis.Int(conn.Do("EXPIRE", n.key, lockTTL))
				if err != nil {
					log.Errorf("Renewing lock for %s failed: %s", n.identifier, err)
					return
				} else if result == 0 {
					log.Errorf("Renewing lock for %s failed: lock disappeared", n.identifier)
					return
				}
				if os.Getenv("DEBUG") != "" {
					log.Debugf("[%s] Lock renewed", n.identifier)
				}
			}
		}
	}()

	return n.done, nil
}

// Release releases the exclusive lock on the mirror
func (n *ClusterLock) Release() {
	close(n.done)
}
