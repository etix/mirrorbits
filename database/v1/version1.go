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

func (v *Version1) Upgrade() error {
	m, err := v.CreateMirrorIndex()
	if err != nil {
		return err
	}

	err = v.RenameKeys(m)
	if err != nil {
		return err
	}

	err = v.FixMirrorID(m)
	if err != nil {
		return err
	}

	err = v.RenameStats(m)
	if err != nil {
		return err
	}

	r := v.Redis.UnblockedGet()
	defer r.Close()

	_, err = r.Do("SET", core.DBVersionKey, 1)
	if err != nil {
		return err
	}

	return nil
}

func (v *Version1) CreateMirrorIndex() (map[int]string, error) {
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

	// Remove the original list of mirrors
	if _, err = conn.Do("DEL", "MIRRORS"); err != nil {
		return m, errors.WithStack(err)
	}

	// Rename the new index
	if _, err = conn.Do("RENAME", "V1_MIRRORS", "MIRRORS"); err != nil {
		return m, errors.WithStack(err)
	}
	return m, nil
}

func (v *Version1) RenameKeys(m map[int]string) error {
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
			conn.Send("RENAME", fmt.Sprintf("FILEINFO_%s_%s", name, file), fmt.Sprintf("FILEINFO_%d_%s", id, file))
		}
		if err := conn.Flush(); err != nil {
			return errors.WithStack(err)
		}

		// Rename the remaing global keys
		conn.Send("RENAME", fmt.Sprintf("MIRROR_%s", name), fmt.Sprintf("MIRROR_%d", id))
		conn.Send("RENAME", fmt.Sprintf("MIRROR_%s_FILES", name), fmt.Sprintf("MIRRORFILES_%d", id))
		conn.Send("RENAME", fmt.Sprintf("HANDLEDFILES_%s", name), fmt.Sprintf("HANDLEDFILES_%d", id))
		if err := conn.Flush(); err != nil {
			return errors.WithStack(err)
		}
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

		// Remove the previous key
		conn.Send("DEL", fmt.Sprintf("FILEMIRRORS_%s", file))
		// Rename the new key
		conn.Send("RENAME", fmt.Sprintf("V1_FILEMIRRORS_%s", file), fmt.Sprintf("FILEMIRRORS_%s", file))

		if err := conn.Flush(); err != nil {
			return errors.WithStack(err)
		}
	}

	return nil
}

func (v *Version1) FixMirrorID(m map[int]string) error {
	conn := v.Redis.UnblockedGet()
	defer conn.Close()

	// Replace ID by the new mirror id
	// Add a field 'name' containing the mirror name
	for id, name := range m {
		conn.Send("HMSET", fmt.Sprintf("MIRROR_%d", id), "ID", id, "name", name)
	}
	if err := conn.Flush(); err != nil {
		return errors.WithStack(err)
	}

	return nil
}

func (v *Version1) RenameStats(m map[int]string) error {
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
			conn.Send("HDEL", key, identifier)

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

			conn.Send("HSET", key, id, value)
		}
		if err := conn.Flush(); err != nil {
			return errors.WithStack(err)
		}
	}

	return nil
}

// IsErrNoSuchKey return true if error is of type "no such key"
func IsErrNoSuchKey(err error) bool {
	// PARSING: "ERR no such key"
	if err != nil && err.Error() == "ERR no such key" {
		return true
	}
	return false
}
