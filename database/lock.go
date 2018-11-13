package database

import (
	"errors"
	"math/rand"
	"strconv"
	"sync"
	"time"

	"github.com/gomodule/redigo/redis"
)

var (
	ErrInvalidLockName = errors.New("invalid lock name")
	ErrAlreadyLocked   = errors.New("lock already acquired")
)

type Lock struct {
	sync.RWMutex
	redis *Redis
	name  string
	value string
	held  bool
}

func init() {
	rand.Seed(time.Now().UnixNano())
}

func (r *Redis) AcquireLock(name string) (*Lock, error) {
	if len(name) == 0 {
		return nil, ErrInvalidLockName
	}

	l := &Lock{
		redis: r,
		name:  "LOCK_" + name,
		value: strconv.Itoa(rand.Int()),
		held:  true,
	}

	conn := r.UnblockedGet()
	defer conn.Close()
	_, err := redis.String(conn.Do("SET", l.name, l.value, "NX", "PX", "5000"))
	if err == redis.ErrNil {
		return nil, ErrAlreadyLocked
	} else if err != nil {
		return nil, err
	}

	// Start the lock keepalive
	go l.keepalive()

	return l, nil
}

func (l *Lock) keepalive() {
	for {
		l.Lock()
		if l.held == false {
			l.Unlock()
			return
		}
		l.Unlock()
		valid, err := l.isValid()
		if err != nil {
			continue
		}
		if !valid {
			l.Lock()
			l.held = false
			l.Unlock()
			return
		}
		conn := l.redis.UnblockedGet()
		ok, err := redis.Bool(conn.Do("PEXPIRE", l.name, "5000"))
		conn.Close()
		if err != nil {
			continue
		}
		if !ok {
			l.held = false
			return
		}
		time.Sleep(1 * time.Second)
	}
}

func (l *Lock) isValid() (bool, error) {
	conn := l.redis.UnblockedGet()
	defer conn.Close()
	value, err := redis.String(conn.Do("GET", l.name))
	if err != nil && err != redis.ErrNil {
		return false, err
	}
	if value != l.value {
		return false, nil
	}
	return true, nil
}

func (l *Lock) Release() {
	l.Lock()
	if l.held == false {
		l.Unlock()
		return
	}
	l.held = false
	l.Unlock()
	conn := l.redis.UnblockedGet()
	defer conn.Close()
	v, _ := redis.String(conn.Do("GET", l.name))
	if v == l.value {
		// Delete the key only if we are still the owner
		conn.Do("DEL", l.name)
	}
}

func (l *Lock) Held() bool {
	l.RLock()
	defer l.RUnlock()
	return l.held
}
