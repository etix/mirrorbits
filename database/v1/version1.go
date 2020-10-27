// Copyright (c) 2014-2020 Ludovic Fauvet
// Licensed under the MIT license

package v1

import (
	"fmt"

	"github.com/etix/mirrorbits/core"
	"github.com/etix/mirrorbits/database/interfaces"
	"github.com/gomodule/redigo/redis"
	"github.com/pkg/errors"
)

// NewUpgraderV1 upgrades the database from version 0 to 1
func NewUpgraderV1(redis interfaces.Redis) *Version1 {
	return &Version1{
		Redis: redis,
	}
}

type Version1 struct {
	Redis interfaces.Redis
}

type actions struct {
	delete []string
	rename map[string]string
}

func (v *Version1) Upgrade() error {
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
	return keys`, 0, "V1_*")

	if err != nil {
		return err
	}

	m, err := v.CreateMirrorIndex(a)
	if err != nil {
		return err
	}

	err = v.RenameKeys(a, m)
	if err != nil {
		return err
	}

	err = v.FixMirrorID(a, m)
	if err != nil {
		return err
	}

	err = v.RenameStats(a, m)
	if err != nil {
		return err
	}

	// Start a transaction to atomically and irrevocably set the new version
	conn.Send("MULTI")

	for k, v := range a.rename {
		conn.Send("RENAME", k, v)
	}

	for _, d := range a.delete {
		do := true
		for _, v := range a.rename {
			if d == v {
				// Abort the operation since this would
				// delete the result of a rename
				do = false
				break
			}
		}
		if do {
			conn.Send("DEL", d)
		}
	}

	conn.Send("SET", core.DBVersionKey, 1)

	// Finalize the transaction
	_, err = conn.Do("EXEC")

	// <-- At this point, if any of the previous mutation failed, it is still
	// safe to run a previous version of mirrorbits.

	return err
}

func (v *Version1) CreateMirrorIndex(a *actions) (map[int]string, error) {
	m := make(map[int]string)

	conn := v.Redis.UnblockedGet()
	defer conn.Close()

	// Get the v0 list of mirrors
	mirrors, err := redis.Strings(conn.Do("LRANGE", "MIRRORS", "0", "-1"))
	if err != nil {
		return m, errors.WithStack(err)
	}

	for _, name := range mirrors {
		// Create a unique ID for the current mirror
		id, err := redis.Int(conn.Do("INCR", "LAST_MID"))
		if err != nil {
			return m, errors.WithStack(err)
		}

		// Assign the ID to the current mirror
		if _, err = conn.Do("HSET", "V1_MIRRORS", id, name); err != nil {
			return m, errors.WithStack(err)
		}

		m[id] = name
	}

	// Prepare for renaming
	a.rename["V1_MIRRORS"] = "MIRRORS"

	return m, nil
}

func (v *Version1) RenameKeys(a *actions, m map[int]string) error {
	conn := v.Redis.UnblockedGet()
	defer conn.Close()

	// Rename all keys to contain the ID instead of the name
	for id, name := range m {
		// Get the list of files known to this mirror
		files, err := redis.Strings(conn.Do("SMEMBERS", fmt.Sprintf("MIRROR_%s_FILES", name)))
		if err == redis.ErrNil || IsErrNoSuchKey(err) {
			continue
		} else if err != nil {
			return errors.WithStack(err)
		}

		// Rename the FILEINFO_<name>_<file> keys
		for _, file := range files {
			a.rename[fmt.Sprintf("FILEINFO_%s_%s", name, file)] = fmt.Sprintf("FILEINFO_%d_%s", id, file)
		}

		// Rename the remaing global keys
		a.rename[fmt.Sprintf("MIRROR_%s_FILES", name)] = fmt.Sprintf("MIRRORFILES_%d", id)
		a.rename[fmt.Sprintf("HANDLEDFILES_%s", name)] = fmt.Sprintf("HANDLEDFILES_%d", id)
		// MIRROR_%s -> MIRROR_%d is handled by FixMirrorID
	}

	// Get the list of files in the local repo
	files, err := redis.Strings(conn.Do("SMEMBERS", "FILES"))
	if err != nil && err != redis.ErrNil {
		return errors.WithStack(err)
	}

	// Rename the keys within FILEMIRRORS_*
	for _, file := range files {
		// Get the list of mirrors having each file
		names, err := redis.Strings(conn.Do("SMEMBERS", fmt.Sprintf("FILEMIRRORS_%s", file)))
		if err != nil {
			return errors.WithStack(err)
		}

		for _, name := range names {
			var id int
			for mid, mname := range m {
				if mname == name {
					id = mid
					break
				}
			}

			if id == 0 {
				continue
			}
			conn.Send("SADD", fmt.Sprintf("V1_FILEMIRRORS_%s", file), id)
		}
		if err := conn.Flush(); err != nil {
			return errors.WithStack(err)
		}

		// Mark the key for renaming
		a.rename[fmt.Sprintf("V1_FILEMIRRORS_%s", file)] = fmt.Sprintf("FILEMIRRORS_%s", file)
	}

	return nil
}

func (v *Version1) FixMirrorID(a *actions, m map[int]string) error {
	conn := v.Redis.UnblockedGet()
	defer conn.Close()

	// Replace ID by the new mirror id
	// Add a field 'name' containing the mirror name
	for id, name := range m {
		err := CopyKey(conn, fmt.Sprintf("MIRROR_%s", name), fmt.Sprintf("V1_MIRROR_%d", id))
		if err != nil {
			return errors.WithStack(err)
		}
		conn.Send("HMSET", fmt.Sprintf("V1_MIRROR_%d", id), "ID", id, "name", name)
		a.rename[fmt.Sprintf("V1_MIRROR_%d", id)] = fmt.Sprintf("MIRROR_%d", id)
		a.delete = append(a.delete, fmt.Sprintf("MIRROR_%s", name))
	}
	if err := conn.Flush(); err != nil {
		return errors.WithStack(err)
	}

	return nil
}

func (v *Version1) RenameStats(a *actions, m map[int]string) error {
	conn := v.Redis.UnblockedGet()
	defer conn.Close()

	keys, err := redis.Strings(conn.Do("KEYS", "STATS_MIRROR_*"))
	if err != nil && err != redis.ErrNil {
		return errors.WithStack(err)
	}

	for _, key := range keys {
		// Here we get two formats:
		// - STATS_MIRROR_*
		// - STATS_MIRROR_BYTES_*
		// and each of them with three differents dates (year, year+month, year+month+day)

		stats, err := redis.StringMap(conn.Do("HGETALL", key))
		if err != nil {
			return errors.WithStack(err)
		}

		for identifier, value := range stats {
			var id int
			for mid, mname := range m {
				if mname == identifier {
					id = mid
					break
				}
			}
			if id == 0 {
				// Mirror does not exist anymore
				// This is expected if mirrors were removed over time
				continue
			}

			conn.Send("HSET", "V1_"+key, id, value)
			a.rename["V1_"+key] = key
		}
		if err := conn.Flush(); err != nil {
			return errors.WithStack(err)
		}
	}

	return nil
}

func CopyKey(conn redis.Conn, src, dst string) error {
	dmp, err := redis.String(conn.Do("DUMP", src))
	if err != nil {
		return err
	}
	_, err = conn.Do("RESTORE", dst, 0, dmp, "REPLACE")
	return err
}

// IsErrNoSuchKey return true if error is of type "no such key"
func IsErrNoSuchKey(err error) bool {
	// PARSING: "ERR no such key"
	if err != nil && err.Error() == "ERR no such key" {
		return true
	}
	return false
}
