// Copyright (c) 2024 Arnaud Rebillout
// Licensed under the MIT license

package v2

import (
	"strings"

	"github.com/etix/mirrorbits/core"
	"github.com/etix/mirrorbits/database/interfaces"
	"github.com/gomodule/redigo/redis"
	"github.com/pkg/errors"
)

// NewUpgraderV2 upgrades the database from version 1 to 2
func NewUpgraderV2(redis interfaces.Redis) *Version2 {
	return &Version2{
		Redis: redis,
	}
}

type Version2 struct {
	Redis interfaces.Redis
}

type actions struct {
	rename map[string]string
}

func (v *Version2) Upgrade() error {
	a := &actions{
		rename: make(map[string]string),
	}

	conn := v.Redis.UnblockedGet()
	defer conn.Close()

	// Erase previous work keys (previous failed upgrade?)
	_, err := conn.Do("EVAL", `
	local keys = redis.call('keys', ARGV[1])
	for i=1,#keys,5000 do
		redis.call('del', unpack(keys, i, math.min(i+4999, #keys)))
	end
	return keys`, 0, "V2_*")

	if err != nil {
		return err
	}

	err = v.UpdateMirrors(a)
	if err != nil {
		return err
	}

	// Start a transaction to atomically and irrevocably set the new version
	conn.Send("MULTI")

	for k, v := range a.rename {
		conn.Send("RENAME", k, v)
	}

	conn.Send("SET", core.DBVersionKey, 2)

	// Finalize the transaction
	_, err = conn.Do("EXEC")

	// <-- At this point, if any of the previous mutation failed, it is still
	// safe to run a previous version of mirrorbits.

	return err
}

func (v *Version2) UpdateMirrors(a *actions) error {
	conn := v.Redis.UnblockedGet()
	defer conn.Close()

	// Get the list of mirrors
	keys, err := redis.Strings(conn.Do("KEYS", "MIRROR_*"))
	if err != nil && err != redis.ErrNil {
		return errors.WithStack(err)
	}

	// Iterate on mirrors
	for _, keyProd := range keys {
		// Copy the key
		key := "V2_" + keyProd
		err := CopyKey(conn, keyProd, key)
		if err != nil {
			return errors.WithStack(err)
		}

		// Get the http url
		url, err := redis.String(conn.Do("HGET", key, "http"))
		if err != nil {
			return errors.WithStack(err)
		}

		// Get the status. Note that the key might not exist if ever
		// the mirror was never enabled or scanned successfully.
		up, err := redis.Bool(conn.Do("HGET", key, "up"))
		if err != nil && err != redis.ErrNil {
			return errors.WithStack(err)
		}
		upExists := true
		if err == redis.ErrNil {
			upExists = false
		}

		// Get the excluded reason. As above: the key might not exist.
		reason, err := redis.String(conn.Do("HGET", key, "excludeReason"))
		if err != nil && err != redis.ErrNil {
			return errors.WithStack(err)
		}
		reasonExists := true
		if err == redis.ErrNil {
			reasonExists = false
		}

		// Start a transaction to do all the changes in one go
		conn.Send("MULTI")

		if strings.HasPrefix(url, "https://") {
			// Update up key if needed
			if upExists {
				conn.Send("HSET", key, "httpsUp", up)
				conn.Send("HDEL", key, "up")
			}
			// Update reason key if needed
			if reasonExists {
				conn.Send("HSET", key, "httpsDownReason", reason)
				conn.Send("HDEL", key, "excludeReason")
			}
		} else {
			// Update up key if needed
			if upExists {
				conn.Send("HSET", key, "httpUp", up)
				conn.Send("HDEL", key, "up")
			}
			// Update reason key if needed
			if reasonExists {
				conn.Send("HSET", key, "httpDownReason", reason)
				conn.Send("HDEL", key, "excludeReason")
			}
		}

		// Finalize the transaction
		_, err = conn.Do("EXEC")
		if err != nil {
			return errors.WithStack(err)
		}

		// Mark the key for renaming
		a.rename[key] = keyProd
	}

	return nil
}

func CopyKey(conn redis.Conn, src, dst string) error {
	// NB: Redis COPY https://redis.io/commands/copy/ is only available
	// since Redis 6.2, released in Feb 2021.  That's a bit too recent,
	// so let's stick with the DUMP/RESTORE combination implemented in
	// this function (and copy/pasted from the v1 database upgrade).
	dmp, err := redis.String(conn.Do("DUMP", src))
	if err != nil {
		return err
	}
	_, err = conn.Do("RESTORE", dst, 0, dmp, "REPLACE")
	return err
}

