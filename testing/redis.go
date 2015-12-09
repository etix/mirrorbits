// Copyright (c) 2014-2015 Ludovic Fauvet
// Licensed under the MIT license

package testing

import (
	"github.com/garyburd/redigo/redis"
	"github.com/rafaeljusto/redigomock"
)

type RedisPoolMock struct {
	Conn *redigomock.Conn
}

func (r *RedisPoolMock) Get() redis.Conn {
	return r.Conn
}

func (r *RedisPoolMock) Close() error {
	return nil
}
