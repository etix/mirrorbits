package interfaces

import "github.com/gomodule/redigo/redis"

type Redis interface {
	Get() redis.Conn
	UnblockedGet() redis.Conn
}
