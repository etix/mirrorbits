// Copyright (c) 2014-2019 Ludovic Fauvet
// Licensed under the MIT license

package interfaces

import "github.com/gomodule/redigo/redis"

type Redis interface {
	Get() redis.Conn
	UnblockedGet() redis.Conn
}
