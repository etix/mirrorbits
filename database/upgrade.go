package database

import (
	"errors"
	"time"

	"github.com/etix/mirrorbits/core"
	"github.com/etix/mirrorbits/database/upgrader"
	"github.com/gomodule/redigo/redis"
)

var (
	ErrUnsupportedVersion = errors.New("unsupported database version, please upgrade mirrorbits")
)

// UpgradeNeeded returns true if a database upgrade is needed
func (r *Redis) UpgradeNeeded() (bool, error) {
	version, err := r.GetDBFormatVersion()
	if err != nil {
		return false, err
	}
	if version > core.DBVersion {
		return false, ErrUnsupportedVersion
	}
	return version != core.DBVersion, nil
}

// GetDBFormatVersion return the current database format version
func (r *Redis) GetDBFormatVersion() (int, error) {
	conn := r.UnblockedGet()
	defer conn.Close()

again:
	version, err := redis.Int(conn.Do("GET", core.DBVersionKey))
	if RedisIsLoading(err) {
		time.Sleep(time.Millisecond * 100)
		goto again
	} else if err == redis.ErrNil {
		found, err := redis.Bool(conn.Do("EXISTS", "MIRRORS"))
		if err != nil {
			return -1, err
		}
		if found {
			return 0, nil
		}
		_, err = conn.Do("SET", core.DBVersionKey, core.DBVersion)
		return core.DBVersion, err
	} else if err != nil {
		return -1, err
	}
	return version, nil
}

// Upgrade starts the upgrade of the database format
func (r *Redis) Upgrade() error {
	version, err := r.GetDBFormatVersion()
	if err != nil {
		return err
	}
	if version > core.DBVersion {
		return ErrUnsupportedVersion
	} else if version == core.DBVersion {
		return nil
	}
	lock, err := r.AcquireLock("upgrade")
	if err != nil {
		return err
	}
	defer lock.Release()

	for i := version + 1; i <= core.DBVersion; i++ {
		u := upgrader.GetUpgrader(r, i)
		if u != nil {
			log.Warningf("Upgrading database from version %d to version %d...", i-1, i)
			if err = u.Upgrade(); err != nil {
				return err
			}
		}
	}

	return nil
}
