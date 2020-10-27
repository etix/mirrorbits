// Copyright (c) 2014-2020 Ludovic Fauvet
// Licensed under the MIT license

package testing

import (
	"github.com/etix/mirrorbits/database"
	"github.com/gomodule/redigo/redis"
	"github.com/rafaeljusto/redigomock"
)

type redisPoolMock struct {
	Conn *redigomock.Conn
}

func (r *redisPoolMock) Get() redis.Conn {
	return r.Conn
}

func (r *redisPoolMock) Close() error {
	return nil
}

// PrepareRedisTest initialize redis tests
func PrepareRedisTest() (*redigomock.Conn, *database.Redis) {
	mock := redigomock.NewConn()

	pool := &redisPoolMock{
		Conn: mock,
	}

	conn := database.NewRedisCustomPool(pool)

	return mock, conn
}
