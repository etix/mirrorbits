// Copyright (c) 2014-2015 Ludovic Fauvet
// Licensed under the MIT license

package testing

import (
	"github.com/etix/mirrorbits/database"
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

func PrepareRedisTest() (*redigomock.Conn, *database.Redis) {
	mock := redigomock.NewConn()

	pool := &RedisPoolMock{
		Conn: mock,
	}

	conn := database.NewRedisCustomPool(pool)

	return mock, conn
}
