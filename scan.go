// Copyright (c) 2014-2015 Ludovic Fauvet
// Licensed under the MIT license

package main

import (
	"errors"
	"fmt"
	"github.com/garyburd/redigo/redis"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

var (
	scanAborted    = errors.New("scan aborted")
	scanInProgress = errors.New("scan already in progress")
)

type ScannerType int8

const (
	RSYNC ScannerType = iota
	FTP
)

type Scanner interface {
	Scan(url, identifier string, conn redis.Conn, stop chan bool) error
}

type filedata struct {
	path    string
	sha1    string
	sha256  string
	md5     string
	size    int64
	modTime time.Time
}

type scan struct {
	redis           *redisobj
	walkSourceFiles []*filedata
	walkRedisConn   redis.Conn

	conn        redis.Conn
	identifier  string
	filesKey    string
	filesTmpKey string
	count       uint
}

func IsScanning(conn redis.Conn, identifier string) (bool, error) {
	return redis.Bool(conn.Do("EXISTS", fmt.Sprintf("SCANNING_%s", identifier)))
}

func Scan(typ ScannerType, r *redisobj, url, identifier string, stop chan bool) error {
	s := &scan{
		redis:      r,
		identifier: identifier,
	}

	var scanner Scanner
	switch typ {
	case RSYNC:
		scanner = &RsyncScanner{
			scan: s,
		}
	case FTP:
		scanner = &FTPScanner{
			scan: s,
		}
	default:
		panic(fmt.Sprintf("Unknown scanner"))
	}

	// Connect to the database
	conn := s.redis.pool.Get()
	defer conn.Close()

	s.conn = conn

	lockKey := fmt.Sprintf("SCANNING_%s", identifier)

	// Try to aquire a lock so we don't have a scanning race
	// from different nodes.
	lock, err := redis.Bool(conn.Do("SETNX", lockKey, 1))
	if err != nil {
		return err
	}
	if lock {
		// Lock aquired.
		defer conn.Do("DEL", lockKey)
		// Make the key expire automatically in case our process gets killed
		conn.Do("EXPIRE", lockKey, 600)
	} else {
		return scanInProgress
	}

	s.setLastSync(conn, identifier, false)

	conn.Send("MULTI")

	s.filesKey = fmt.Sprintf("MIRROR_%s_FILES", identifier)
	s.filesTmpKey = fmt.Sprintf("MIRROR_%s_FILES_TMP", identifier)

	// Remove any left over
	conn.Send("DEL", s.filesTmpKey)

	err = scanner.Scan(url, identifier, conn, stop)
	if err != nil {
		// Discard MULTI
		s.ScannerDiscard()

		// Remove the temporary key
		conn.Do("DEL", s.filesTmpKey)

		log.Error("[%s] %s", identifier, err.Error())
		return err
	}

	// Exec multi
	s.ScannerCommit()

	// Get the list of files no more present on this mirror
	toremove, err := redis.Values(conn.Do("SDIFF", s.filesKey, s.filesTmpKey))
	if err != nil {
		return err
	}

	// Remove this mirror from the given file SET
	if len(toremove) > 0 {
		conn.Send("MULTI")
		for _, e := range toremove {
			log.Debug("[%s] Removing %s from mirror", identifier, e)
			conn.Send("SREM", fmt.Sprintf("FILEMIRRORS_%s", e), identifier)
			conn.Send("DEL", fmt.Sprintf("FILEINFO_%s_%s", identifier, e))
			// Publish update
			SendPublish(conn, MIRROR_FILE_UPDATE, fmt.Sprintf("%s %s", identifier, e))

		}
		_, err = conn.Do("EXEC")
		if err != nil {
			return err
		}
	}

	// Finally rename the temporary sets containing the list
	// of files for this mirror to the production key
	_, err = conn.Do("RENAME", s.filesTmpKey, s.filesKey)
	if err != nil {
		return err
	}

	sinterKey := fmt.Sprintf("HANDLEDFILES_%s", identifier)

	// Count the number of files known on the remote end
	common, _ := redis.Int64(conn.Do("SINTERSTORE", sinterKey, "FILES", s.filesKey))

	if err != nil {
		return err
	}

	s.setLastSync(conn, identifier, true)
	log.Info("[%s] Indexed %d files (%d known), %d removed", identifier, s.count, common, len(toremove))
	return nil
}

func (s *scan) ScannerAddFile(f filedata) {
	s.count++

	// Add all the files to a temporary key
	s.conn.Send("SADD", s.filesTmpKey, f.path)

	// Mark the file as being supported by this mirror
	rk := fmt.Sprintf("FILEMIRRORS_%s", f.path)
	s.conn.Send("SADD", rk, s.identifier)

	// Save the size of the current file found on this mirror
	ik := fmt.Sprintf("FILEINFO_%s_%s", s.identifier, f.path)
	s.conn.Send("HSET", ik, "size", f.size)

	// Publish update
	SendPublish(s.conn, MIRROR_FILE_UPDATE, fmt.Sprintf("%s %s", s.identifier, f.path))
}

func (s *scan) ScannerDiscard() {
	s.conn.Do("DISCARD")
}

func (s *scan) ScannerCommit() error {
	_, err := s.conn.Do("EXEC")
	return err
}

func (s *scan) setLastSync(conn redis.Conn, identifier string, successful bool) error {
	now := time.Now().UTC().Unix()

	conn.Send("MULTI")

	// Set the last sync time
	conn.Send("HSET", fmt.Sprintf("MIRROR_%s", identifier), "lastSync", now)

	// Set the last successful sync time
	if successful {
		conn.Send("HSET", fmt.Sprintf("MIRROR_%s", identifier), "lastSuccessfulSync", now)
	}

	_, err := conn.Do("EXEC")

	// Publish an update on redis
	Publish(conn, MIRROR_UPDATE, identifier)

	return err
}

// Walk inside the source/reference repository
func (s *scan) walkSource(path string, f os.FileInfo, err error) error {
	if f == nil || f.IsDir() || f.Mode()&os.ModeSymlink != 0 {
		return nil
	}

	d := new(filedata)
	d.path = path[len(GetConfig().Repository):]
	d.size = f.Size()
	d.modTime = f.ModTime()

	// Get the previous file properties
	properties, err := redis.Strings(s.walkRedisConn.Do("HMGET", fmt.Sprintf("FILE_%s", d.path), "size", "modTime", "sha1", "sha256", "md5"))
	if err != nil && err != redis.ErrNil {
		return err
	} else if len(properties) < 5 {
		// This will force a rehash
		properties = make([]string, 5)
	}

	size, _ := strconv.ParseInt(properties[0], 10, 64)
	modTime, _ := time.Parse("2006-01-02 15:04:05.999999999 -0700 MST", properties[1])
	sha1 := properties[2]
	sha256 := properties[3]
	md5 := properties[4]

	rehash := (GetConfig().Hashes.SHA1 && len(sha1) == 0) ||
		(GetConfig().Hashes.SHA256 && len(sha256) == 0) ||
		(GetConfig().Hashes.MD5 && len(md5) == 0)

	if rehash || size != d.size || !modTime.Equal(d.modTime) {
		h, err := hashFile(GetConfig().Repository + d.path)
		if err != nil {
			log.Warning("%s: hashing failed: %s", d.path, err.Error())
		} else {
			d.sha1 = h.Sha1
			d.sha256 = h.Sha256
			d.md5 = h.Md5
			if len(d.sha1) > 0 {
				log.Info("%s: SHA1 %s", d.path, d.sha1)
			}
			if len(d.sha256) > 0 {
				log.Info("%s: SHA256 %s", d.path, d.sha256)
			}
			if len(d.md5) > 0 {
				log.Info("%s: MD5 %s", d.path, d.md5)
			}
		}
	} else {
		d.sha1 = sha1
		d.sha256 = sha256
		d.md5 = md5
	}

	s.walkSourceFiles = append(s.walkSourceFiles, d)
	return nil
}

func ScanSource(r *redisobj, stop chan bool) (err error) {
	s := &scan{
		redis: r,
	}

	s.walkRedisConn = s.redis.pool.Get()
	defer s.walkRedisConn.Close()
	if err != nil {
		return fmt.Errorf("redis %s", err.Error())
	}
	defer s.walkRedisConn.Close()

	s.walkSourceFiles = make([]*filedata, 0, 1000)
	defer func() {
		// Reset the slice so it can be garbage collected
		s.walkSourceFiles = nil
	}()

	//TODO lock atomically inside redis to avoid two simultanous scan

	if _, err := os.Stat(GetConfig().Repository); os.IsNotExist(err) {
		return fmt.Errorf("%s: No such file or directory", GetConfig().Repository)
	}

	log.Info("[source] Scanning the filesystem...")
	err = filepath.Walk(GetConfig().Repository, s.walkSource)
	if isStopped(stop) {
		return scanAborted
	}
	if err != nil {
		return err
	}
	log.Info("[source] Indexing the files...")

	s.walkRedisConn.Send("MULTI")

	// Remove any left over
	s.walkRedisConn.Send("DEL", "FILES_TMP")

	// Add all the files to a temporary key
	count := 0
	for _, e := range s.walkSourceFiles {
		s.walkRedisConn.Send("SADD", "FILES_TMP", e.path)
		count++
	}

	_, err = s.walkRedisConn.Do("EXEC")
	if err != nil {
		return err
	}

	// Do a diff between the sets to get the removed files
	toremove, err := redis.Values(s.walkRedisConn.Do("SDIFF", "FILES", "FILES_TMP"))

	// Create/Update the files' hash keys with the fresh infos
	s.walkRedisConn.Send("MULTI")
	for _, e := range s.walkSourceFiles {
		s.walkRedisConn.Send("HMSET", fmt.Sprintf("FILE_%s", e.path),
			"size", e.size,
			"modTime", e.modTime,
			"sha1", e.sha1,
			"sha256", e.sha256,
			"md5", e.md5)

		// Publish update
		SendPublish(s.walkRedisConn, FILE_UPDATE, e.path)
	}

	// Remove old keys
	if len(toremove) > 0 {
		for _, e := range toremove {
			s.walkRedisConn.Send("DEL", fmt.Sprintf("FILE_%s", e))

			// Publish update
			SendPublish(s.walkRedisConn, FILE_UPDATE, fmt.Sprintf("%s", e))
		}
	}

	// Finally rename the temporary sets containing the list
	// of files to the production key
	s.walkRedisConn.Send("RENAME", "FILES_TMP", "FILES")

	_, err = s.walkRedisConn.Do("EXEC")
	if err != nil {
		return err
	}

	log.Info("[source] Scanned %d files", count)

	return nil
}
