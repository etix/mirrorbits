package database

import (
	"errors"
	"time"

	"github.com/etix/mirrorbits/database/upgrader"
	"github.com/gomodule/redigo/redis"
)

const (
	dbVersion = 1 // Current DB format version
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
	if version > dbVersion {
		return false, ErrUnsupportedVersion
	}
	return version != dbVersion, nil
}

// GetDBFormatVersion return the current database format version
func (r *Redis) GetDBFormatVersion() (int, error) {
	conn := r.Get()
	defer conn.Close()

again:
	version, err := redis.Int(conn.Do("GET", "MIRRORBITS_DB_VERSION"))
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
		} else {
			return dbVersion, nil
		}
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
	if version > dbVersion {
		return ErrUnsupportedVersion
	} else if version == dbVersion {
		return nil
	}
	lock, err := r.AcquireLock("upgrade")
	if err != nil {
		return err
	}
	defer lock.Release()

	for i := version + 1; i <= dbVersion; i++ {
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
