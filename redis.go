// Copyright (c) 2014 Ludovic Fauvet
// Licensed under the MIT license

package main

import (
	"errors"
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
	c, err := redis.DialTimeout("tcp", GetConfig().RedisAddress, redisConnectionTimeout, redisReadWriteTimeout, redisReadWriteTimeout)

	if err != nil {
		return nil, err
	}
	if GetConfig().RedisPassword != "" {
		if _, err := c.Do("AUTH", GetConfig().RedisPassword); err != nil {
			c.Close()
			return nil, err
		}
	}
	return c, err
}
