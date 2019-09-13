// Copyright (c) 2014-2019 Ludovic Fauvet
// Licensed under the MIT license

package database

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	. "github.com/etix/mirrorbits/config"
	"github.com/etix/mirrorbits/core"
	"github.com/gomodule/redigo/redis"
	"github.com/rafaeljusto/redigomock"
)

const (
	redisConnectionTimeout = 200 * time.Millisecond
	redisReadWriteTimeout  = 300 * time.Second
)

var (
	// ErrUnreachable is returned when the endpoint is not reachable
	ErrUnreachable = errors.New("redis endpoint unreachable")
	// ErrRedisUpgradeRequired is returned when the redis server is running an unsupported version
	ErrRedisUpgradeRequired = errors.New("unsupported Redis version")
)

type redisPool interface {
	Get() redis.Conn
	Close() error
}

// Redis is the instance object of the redis database
type Redis struct {
	pool            redisPool
	Pubsub          *Pubsub
	failure         bool
	failureState    sync.RWMutex
	knownMaster     string
	knownMasterLock sync.Mutex
	stop            chan bool
	ready           chan struct{}
}

// NewRedis returns a new instance of the redis database
func NewRedis() *Redis {
	r := NewRedisCustomPool(nil)

	// Asynchronous db update handler
	go r.dbUpdateHandler()

	return r
}

// NewRedisCustomPool returns a new instance of the redis database
// using a custom pool
func NewRedisCustomPool(pool redisPool) *Redis {
	r := &Redis{
		stop:  make(chan bool),
		ready: make(chan struct{}),
	}

	if pool != nil {
		// Check if we are running inside `go test`
		if _, ok := pool.Get().(*redigomock.Conn); ok {
			// Close ready since we are running a mock
			close(r.ready)
		}
		r.pool = pool
	} else {
		r.pool = &redis.Pool{
			MaxIdle:     10,
			IdleTimeout: 240 * time.Second,
			Dial: func() (redis.Conn, error) {
				conn, err := r.Connect()

				switch err {
				case nil:
					r.setFailureState(false)
				default:
					r.setFailureState(true)
				}

				if r.checkVersion(conn) == ErrRedisUpgradeRequired {
					log.Fatalf("Unsupported Redis version, please upgrade to Redis >= %s", core.RedisMinimumVersion)
				}

				return conn, err
			},
			TestOnBorrow: func(c redis.Conn, t time.Time) error {
				_, err := c.Do("PING")
				if RedisIsLoading(err) {
					return nil
				}
				return err
			},
		}
	}

	go r.connRecover()

	return r
}

// Get returns a redis connection from the pool
func (r *Redis) Get() redis.Conn {
	select {
	case <-r.ready:
	default:
		return &NotReadyError{}
	}
	return r.pool.Get()
}

// UnblockedGet returns a redis connection from the pool even
// if the database checks and/or upgrade are not finished.
func (r *Redis) UnblockedGet() redis.Conn {
	return r.pool.Get()
}

// Close closes all connections to the redis database
func (r *Redis) Close() {
	select {
	case _, _ = <-r.stop:
		return
	default:
		log.Debug("Closing databases connections")
		r.Pubsub.Close()
		r.pool.Close()
		close(r.stop)
	}
}

// ConnectPubsub initiates the connection to the pubsub
func (r *Redis) ConnectPubsub() {
	if r.Pubsub == nil {
		r.Pubsub = NewPubsub(r)
	}
}

// CheckVersion checks if the redis server version is supported
func (r *Redis) CheckVersion() error {
	c := r.UnblockedGet()
	defer c.Close()
	return r.checkVersion(c)
}

func (r *Redis) checkVersion(conn redis.Conn) error {
	if conn == nil {
		return ErrUnreachable
	}
	info, err := parseInfo(conn.Do("INFO", "server"))
	if err == nil {
		if parseVersion(info["redis_version"]) < parseVersion(core.RedisMinimumVersion) {
			return ErrRedisUpgradeRequired
		}
	}
	return err
}

// Connect initiates a new connection to the redis server
func (r *Redis) Connect() (redis.Conn, error) {
	sentinels := GetConfig().RedisSentinels

	if len(sentinels) > 0 {

		if len(GetConfig().RedisSentinelMasterName) == 0 {
			r.logError("Config: RedisSentinelMasterName cannot be empty!")
			goto single
		}

		for _, s := range sentinels {
			log.Debugf("Connecting to redis sentinel %s", s.Host)
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
			if err = r.selectDB(cm); err != nil {
				c.Close()
				return nil, err
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
		return nil, ErrUnreachable
	}

	if len(sentinels) > 0 && r.Failure() == false {
		log.Warning("No redis master available, trying using the configured RedisAddress as fallback")
	}

	c, err := r.connectTo(GetConfig().RedisAddress)
	if err != nil {
		return nil, err
	}
	if err = r.auth(c); err != nil {
		c.Close()
		return nil, err
	}
	if err = r.selectDB(c); err != nil {
		c.Close()
		return nil, err
	}
	role, err := r.askRole(c)
	if err != nil {
		r.logError("Redis master: %s", err.Error())
		return nil, ErrUnreachable
	}
	if role != "master" {
		r.logError("Redis master: %s is not a master but a %s", GetConfig().RedisAddress, role)
		return nil, ErrUnreachable
	}
	r.printConnectedMaster(GetConfig().RedisAddress)
	return c, err

}

func (r *Redis) connectTo(address string) (redis.Conn, error) {
	return redis.Dial("tcp", address,
		redis.DialConnectTimeout(redisConnectionTimeout),
		redis.DialReadTimeout(redisReadWriteTimeout),
		redis.DialWriteTimeout(redisReadWriteTimeout))
}

func (r *Redis) askRole(c redis.Conn) (string, error) {
	roleReply, err := redis.Values(c.Do("ROLE"))
	if err != nil {
		return "", err
	}
	role, err := redis.String(roleReply[0], err)
	return role, err
}

func (r *Redis) auth(c redis.Conn) (err error) {
	if GetConfig().RedisPassword != "" {
		_, err = c.Do("AUTH", GetConfig().RedisPassword)
	}
	return
}

func (r *Redis) selectDB(c redis.Conn) (err error) {
	_, err = c.Do("SELECT", GetConfig().RedisDB)
	return
}

func (r *Redis) logError(format string, args ...interface{}) {
	if r.Failure() {
		log.Debugf(format, args...)
	} else {
		log.Errorf(format, args...)
	}
}

func (r *Redis) printConnectedMaster(address string) {
	r.knownMasterLock.Lock()
	defer r.knownMasterLock.Unlock()
	if address != r.knownMaster && core.Daemon {
		r.knownMaster = address
		log.Infof("Connected to redis master %s", address)
	} else {
		log.Debugf("Connected to redis master %s", address)
	}
}

func (r *Redis) setFailureState(failure bool) {
	r.failureState.Lock()
	r.failure = failure
	r.failureState.Unlock()
}

// Failure returns true if the connection is in a failure state
func (r *Redis) Failure() bool {
	r.failureState.RLock()
	defer r.failureState.RUnlock()
	return r.failure
}

func (r *Redis) connRecover() {
	ticker := time.NewTicker(1 * time.Second)

	for {
		select {
		case <-r.stop:
			return
		case <-ticker.C:
			if r.Failure() {
				if conn := r.Get(); conn != nil {
					// A successful Get() request will automatically unlock
					// other services waiting for a working connection.
					// This is only a way to ensure they wont wait forever.
					if conn.Err() != nil {
						log.Warningf("Database is down: %s", conn.Err().Error())
					}
					conn.Close()
				}
			}
		}
	}
}

func (r *Redis) dbUpdateHandler() {
	var logOnce sync.Once

again:
	upneeded, err := r.UpgradeNeeded()
	if err != nil {
		time.Sleep(100 * time.Millisecond)
		goto again
	}
	if upneeded {
		t := time.Now()
		err = r.Upgrade()
		if err == ErrAlreadyLocked {
			logOnce.Do(func() {
				log.Warning("Database upgrade running. Waiting for completion...")
			})
			time.Sleep(100 * time.Millisecond)
			goto again
		} else if err != nil {
			log.Fatalf("Upgrade failed: %+v", err)
		}
		log.Infof("Database upgrade successful (took %s), starting normally", time.Since(t).Round(time.Millisecond))
	}
	close(r.ready)
}

// RedisIsLoading returns true if the error is of type LOADING
func RedisIsLoading(err error) bool {
	// PARSING: "LOADING Redis is loading the dataset in memory"
	if err != nil && strings.HasPrefix(err.Error(), "LOADING") {
		return true
	}
	return false
}

func parseVersion(version string) int64 {
	s := strings.Split(version, ".")
	format := fmt.Sprintf("%%s%%0%ds", 2)

	var v string
	for _, value := range s {
		v = fmt.Sprintf(format, v, value)
	}

	var result int64
	var err error
	if result, err = strconv.ParseInt(v, 10, 64); err != nil {
		return -1
	}
	return result
}

func parseInfo(i interface{}, err error) (map[string]string, error) {
	v, err := redis.String(i, err)
	if err != nil {
		return nil, err
	}

	m := make(map[string]string)
	lines := strings.Split(v, "\r\n")

	for _, l := range lines {
		if strings.HasPrefix(l, "#") {
			continue
		}

		kv := strings.SplitN(l, ":", 2)
		if len(kv) < 2 {
			continue
		}

		m[kv[0]] = kv[1]
	}

	return m, nil
}
