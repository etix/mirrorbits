// Copyright (c) 2014-2017 Ludovic Fauvet
// Licensed under the MIT license

package network

import (
    "errors"
    "github.com/etix/mirrorbits/database"
    "github.com/garyburd/redigo/redis"
    "os"
    "time"
)

type ClusterLock struct {
    redis      *database.Redis
    key        string
    identifier string
    done       chan struct{}
}

func NewClusterLock(redis *database.Redis, key, identifier string) *ClusterLock {
    return &ClusterLock{
        redis:      redis,
        key:        key,
        identifier: identifier,
    }
}

func (n *ClusterLock) Get() (<-chan struct{}, error) {
    if n.done != nil {
        return nil, errors.New("lock already in use")
    }

    conn := n.redis.Get()
    defer conn.Close()

    if conn.Err() != nil {
        return nil, conn.Err()
    }

    _, err := redis.String(conn.Do("SET", n.key, 1, "NX", "EX", 10))
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
            case <-time.After(5 * time.Second):
                result, err := redis.Int(conn.Do("EXPIRE", n.key, 10))
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

func (n *ClusterLock) Release() {
    close(n.done)
}
