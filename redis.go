// Copyright (c) 2014-2015 Ludovic Fauvet
// Licensed under the MIT license

package main

import (
	"errors"
	"fmt"
	"github.com/garyburd/redigo/redis"
	"sync"
	"time"
)

const (
	redisConnectionTimeout = 200 * time.Millisecond
	redisReadWriteTimeout  = 300 * time.Second
)

var (
	errUnreachable = errors.New("endpoint unreachable")
)

type redisobj struct {
	pool         *redis.Pool
	pubsub       *Pubsub
	failure      bool
	failureState sync.RWMutex
	knownMaster  string
}

func NewRedis() *redisobj {
	r := &redisobj{}

	r.pool = &redis.Pool{
		MaxIdle:     10,
		IdleTimeout: 240 * time.Second,
		Dial: func() (redis.Conn, error) {
			return r.connect()
		},
		TestOnBorrow: func(c redis.Conn, t time.Time) error {
			_, err := c.Do("PING")
			return err
		},
	}

	return r
}

func (r *redisobj) Close() {
	r.pool.Close()
	//TODO close pubsub
}

func (r *redisobj) ConnectPubsub() {
	if r.pubsub == nil {
		r.pubsub = NewPubsub(r)
	}
}

func (r *redisobj) connect() (redis.Conn, error) {
	sentinels := GetConfig().RedisSentinels

	if len(sentinels) > 0 {

		if len(GetConfig().RedisSentinelMasterName) == 0 {
			r.logError("Config: RedisSentinelMasterName cannot be empty!")
			goto single
		}

		for _, s := range sentinels {
			log.Debug("Connecting to redis sentinel %s", s.Host)
			var master []string
			var masterhost string
			var cm redis.Conn

			c, err := r.connectTo(s.Host)
			if err != nil {
				r.logError("Sentinel: %s", err.Error())
				continue
			}
			//AUTH?
			role, err := r.askRole(c)
			if err != nil {
				r.logError("Sentinel: %s", err.Error())
				goto closeSentinel
			}
			if role != "sentinel" {
				r.logError("Sentinel: %s is not a sentinel but a %s", s.Host, role)
				goto closeSentinel
			}

			master, err = redis.Strings(c.Do("SENTINEL", "get-master-addr-by-name", GetConfig().RedisSentinelMasterName))
			if err == redis.ErrNil {
				r.logError("Sentinel: %s doesn't know the master-name %s", s.Host, GetConfig().RedisSentinelMasterName)
				goto closeSentinel
			} else if err != nil {
				r.logError("Sentinel: %s", err.Error())
				goto closeSentinel
			}

			masterhost = fmt.Sprintf("%s:%s", master[0], master[1])

			cm, err = r.connectTo(masterhost)
			if err != nil {
				r.logError("Redis master: %s", err.Error())
				goto closeSentinel
			}

			if r.auth(cm) != nil {
				r.logError("Redis master: auth failed")
				goto closeMaster
			}

			role, err = r.askRole(cm)
			if err != nil {
				r.logError("Redis master: %s", err.Error())
				goto closeMaster
			}
			if role != "master" {
				r.logError("Redis master: %s is not a master but a %s", masterhost, role)
				goto closeMaster
			}

			// Close the connection to the sentinel
			c.Close()
			r.setFailureState(false)

			r.printConnectedMaster(masterhost)
			return cm, nil

		closeMaster:
			cm.Close()

		closeSentinel:
			c.Close()
		}
	}

single:

	if len(GetConfig().RedisAddress) == 0 {
		if len(sentinels) == 0 {
			log.Error("No redis master available")
		}
		r.setFailureState(true)
		return nil, errUnreachable
	}

	if r.getFailureState() == false {
		log.Warning("No redis master available, trying using the configured RedisAddress as fallback")
	}

	c, err := r.connectTo(GetConfig().RedisAddress)
	if err != nil {
		return nil, err
	}
	if r.auth(c); err != nil {
		c.Close()
		return nil, err
	}
	role, err := r.askRole(c)
	if err != nil {
		r.logError("Redis master: %s", err.Error())
		r.setFailureState(true)
		return nil, errUnreachable
	}
	if role != "master" {
		r.logError("Redis master: %s is not a master but a %s", GetConfig().RedisAddress, role)
		r.setFailureState(true)
		return nil, errUnreachable
	}
	r.setFailureState(false)
	r.printConnectedMaster(GetConfig().RedisAddress)
	return c, err

}

func (r *redisobj) connectTo(address string) (redis.Conn, error) {
	return redis.DialTimeout("tcp", address, redisConnectionTimeout, redisReadWriteTimeout, redisReadWriteTimeout)
}

func (r *redisobj) askRole(c redis.Conn) (string, error) {
	roleReply, err := redis.Values(c.Do("ROLE"))
	role, err := redis.String(roleReply[0], err)
	return role, err
}

func (r *redisobj) auth(c redis.Conn) (err error) {
	if GetConfig().RedisPassword != "" {
		_, err = c.Do("AUTH", GetConfig().RedisPassword)
	}
	return
}

func (r *redisobj) logError(format string, args ...interface{}) {
	if r.getFailureState() == true {
		log.Debug(format, args...)
	} else {
		log.Error(format, args...)
	}
}

func (r *redisobj) printConnectedMaster(address string) {
	if address != r.knownMaster && daemon {
		r.knownMaster = address
		log.Info("Connected to redis master %s", address)
	} else {
		log.Debug("Connected to redis master %s", address)
	}
}

func (r *redisobj) setFailureState(failure bool) {
	r.failureState.Lock()
	r.failure = failure
	r.failureState.Unlock()
}

func (r *redisobj) getFailureState() bool {
	r.failureState.RLock()
	defer r.failureState.RUnlock()
	return r.failure
}

// RedisIsLoading returns true if the error is of type LOADING
func RedisIsLoading(err error) bool {
	// PARSING: "LOADING Redis is loading the dataset in memory"
	if err != nil && err.Error()[:7] == "LOADING" {
		return true
	}
	return false
}
