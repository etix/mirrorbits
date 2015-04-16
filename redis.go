// Copyright (c) 2014 Ludovic Fauvet
// Licensed under the MIT license

package main

import (
	"errors"
	"fmt"
	"github.com/garyburd/redigo/redis"
	"time"
)

const (
	redisConnectionTimeout = 200 * time.Millisecond
	redisReadWriteTimeout  = 5000 * time.Millisecond
)

var (
	errUnreachable = errors.New("endpoint unreachable")
)

type redisobj struct {
	pool *redis.Pool
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

func (r *redisobj) connect() (redis.Conn, error) {
	sentinels := GetConfig().RedisSentinels

	if len(sentinels) > 0 {

		if len(GetConfig().RedisSentinelMasterName) == 0 {
			log.Error("Config: RedisSentinelMasterName cannot be empty!")
			goto single
		}

		for _, s := range sentinels {
			log.Debug("Connecting to redis sentinel %s", s.Host)
			var master []string
			var masterhost string
			var cm redis.Conn

			c, err := redis.DialTimeout("tcp", s.Host, redisConnectionTimeout, redisReadWriteTimeout, redisReadWriteTimeout)
			if err != nil {
				log.Error("Sentinel: %s", err.Error())
				continue
			}
			//AUTH?
			role, err := r.askRole(c)
			if err != nil {
				log.Error("Sentinel: %s", err.Error())
				goto closeSentinel
			}
			if role != "sentinel" {
				log.Error("Sentinel: %s is not a sentinel but a %s", s.Host, role)
				goto closeSentinel
			}

			master, err = redis.Strings(c.Do("SENTINEL", "get-master-addr-by-name", GetConfig().RedisSentinelMasterName))
			if err == redis.ErrNil {
				log.Error("Sentinel: %s doesn't know the master-name %s", s.Host, GetConfig().RedisSentinelMasterName)
				goto closeSentinel
			} else if err != nil {
				log.Error("Sentinel: %s", err.Error())
				goto closeSentinel
			}

			masterhost = fmt.Sprintf("%s:%s", master[0], master[1])

			cm, err = redis.DialTimeout("tcp", masterhost, redisConnectionTimeout, redisReadWriteTimeout, redisReadWriteTimeout)
			if err != nil {
				log.Error("Redis master: %s", err.Error())
				goto closeSentinel
			}

			if r.auth(cm) != nil {
				log.Error("Redis master: auth failed")
				goto closeMaster
			}

			role, err = r.askRole(cm)
			if err != nil {
				log.Error("Redis master: %s", err.Error())
				goto closeMaster
			}
			if role != "master" {
				log.Error("Redis master: %s is not a master but a %s", masterhost, role)
				goto closeMaster
			}

			// Close the connection to the sentinel
			c.Close()

			log.Debug("Connected to redis master %s", masterhost)
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
		return nil, errUnreachable
	}

	log.Warning("No redis master available, trying using the configured RedisAddress as fallback")

	c, err := redis.DialTimeout("tcp", GetConfig().RedisAddress, redisConnectionTimeout, redisReadWriteTimeout, redisReadWriteTimeout)
	if err != nil {
		return nil, err
	}
	if r.auth(c); err != nil {
		c.Close()
		return nil, err
	}
	log.Debug("Connected to redis master %s", GetConfig().RedisAddress)
	return c, err

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
