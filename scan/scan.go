// Copyright (c) 2014-2017 Ludovic Fauvet
// Licensed under the MIT license

package scan

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	. "github.com/etix/mirrorbits/config"
	"github.com/etix/mirrorbits/database"
	"github.com/etix/mirrorbits/filesystem"
	"github.com/etix/mirrorbits/network"
	"github.com/etix/mirrorbits/utils"
	"github.com/garyburd/redigo/redis"
	"github.com/op/go-logging"
)

var (
	// ErrScanAborted is returned when a scan is aborted by the user
	ErrScanAborted = errors.New("scan aborted")
	// ErrScanInProgress is returned when a scan is started while another is already in progress
	ErrScanInProgress = errors.New("scan already in progress")

	log = logging.MustGetLogger("main")
)

// ScannerType holds the type of scanner in use
type ScannerType int8

const (
	// RSYNC represents an rsync scanner
	RSYNC ScannerType = iota
	// FTP represents an ftp scanner
	FTP
)

// Scanner is the interface that all scanners must implement
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
	redis *database.Redis

	conn        redis.Conn
	identifier  string
	filesTmpKey string
	count       uint
}

type DummyFile struct {
	Size    int64  `json:"Size"`
	ModTime string `json:"ModTime"`
	Sha1    string `json:"Sha1"`
	Sha256  string `json:"Sha256"`
	Md5     string `json:"Md5"`
}

// IsScanning returns true is a scan is already in progress for the given mirror
func IsScanning(conn redis.Conn, identifier string) (bool, error) {
	return redis.Bool(conn.Do("EXISTS", fmt.Sprintf("SCANNING_%s", identifier)))
}

// Scan starts a scan of the given mirror
func Scan(typ ScannerType, r *database.Redis, url, identifier string, stop chan bool) error {
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
	conn := s.redis.Get()
	defer conn.Close()

	s.conn = conn

	// Try to acquire a lock so we don't have a scanning race
	// from different nodes.
	// Also make the key expire automatically in case our process
	// gets killed.
	lock := network.NewClusterLock(s.redis, fmt.Sprintf("SCANNING_%s", identifier), identifier)

	done, err := lock.Get()
	if err != nil {
		return err
	} else if done == nil {
		return ErrScanInProgress
	}

	defer lock.Release()

	s.setLastSync(conn, identifier, false)

	conn.Send("MULTI")

	filesKey := fmt.Sprintf("MIRROR_%s_FILES", identifier)
	s.filesTmpKey = fmt.Sprintf("MIRROR_%s_FILES_TMP", identifier)

	// Remove any left over
	conn.Send("DEL", s.filesTmpKey)

	err = scanner.Scan(url, identifier, conn, stop)
	if err != nil {
		// Discard MULTI
		s.ScannerDiscard()

		// Remove the temporary key
		conn.Do("DEL", s.filesTmpKey)

		log.Errorf("[%s] %s", identifier, err.Error())
		return err
	}

	// Exec multi
	s.ScannerCommit()

	// Get the list of files no more present on this mirror
	toremove, err := redis.Values(conn.Do("SDIFF", filesKey, s.filesTmpKey))
	if err != nil {
		return err
	}

	// Remove this mirror from the given file SET
	if len(toremove) > 0 {
		conn.Send("MULTI")
		for _, e := range toremove {
			log.Debugf("[%s] Removing %s from mirror", identifier, e)
			conn.Send("SREM", fmt.Sprintf("FILEMIRRORS_%s", e), identifier)
			conn.Send("DEL", fmt.Sprintf("FILEINFO_%s_%s", identifier, e))
			// Publish update
			database.SendPublish(conn, database.MIRROR_FILE_UPDATE, fmt.Sprintf("%s %s", identifier, e))

		}
		_, err = conn.Do("EXEC")
		if err != nil {
			return err
		}
	}

	// Finally rename the temporary sets containing the list
	// of files for this mirror to the production key
	if s.count > 0 {
		_, err = conn.Do("RENAME", s.filesTmpKey, filesKey)
		if err != nil {
			return err
		}
	}

	sinterKey := fmt.Sprintf("HANDLEDFILES_%s", identifier)

	// Count the number of files known on the remote end
	common, _ := redis.Int64(conn.Do("SINTERSTORE", sinterKey, "FILES", filesKey))

	if err != nil {
		return err
	}

	s.setLastSync(conn, identifier, true)
	log.Infof("[%s] Indexed %d files (%d known), %d removed", identifier, s.count, common, len(toremove))
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
	database.SendPublish(s.conn, database.MIRROR_FILE_UPDATE, fmt.Sprintf("%s %s", s.identifier, f.path))
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
	database.Publish(conn, database.MIRROR_UPDATE, identifier)

	return err
}

type sourcescanner struct {
	dummyFile bool
}

// Walk inside the source/reference repository
func (s *sourcescanner) walkSource(conn redis.Conn, path string, f os.FileInfo, rehash bool, err error) (*filedata, error) {
	if f == nil || f.IsDir() || f.Mode()&os.ModeSymlink != 0 {
		return nil, nil
	}

	var dfData DummyFile
	dummyFile := s.dummyFile

	d := new(filedata)
	d.path = path[len(GetConfig().Repository):]

	if dummyFile {
		file, err := os.Open(path)
		if err != nil {
			log.Errorf(err.Error())
		}
		dec := json.NewDecoder(file)
		// read open bracket, when there is none, the file is not a json.
		// Fallback to normal mode.
		_, err = dec.Token()
		if err != nil {
			goto skipdec
		}
		err = dec.Decode(&dfData)
	skipdec:
		if err != nil {
			log.Debugf("Failed to read file: %s", err.Error())
			dummyFile = false
			d.size = f.Size()
			d.modTime = f.ModTime()
			goto skip
		}

		d.size = dfData.Size
		d.modTime, err = time.Parse("2006-01-02 15:04:05.999999999 -0700 MST", dfData.ModTime)
		if err != nil {
			log.Errorf(err.Error())
		}
	} else {
		d.size = f.Size()
		d.modTime = f.ModTime()
	}

skip:
	// Get the previous file properties
	properties, err := redis.Strings(conn.Do("HMGET", fmt.Sprintf("FILE_%s", d.path), "size", "modTime", "sha1", "sha256", "md5"))
	if err != nil && err != redis.ErrNil {
		return nil, err
	} else if len(properties) < 5 {
		// This will force a rehash
		properties = make([]string, 5)
	}

	size, _ := strconv.ParseInt(properties[0], 10, 64)
	modTime, _ := time.Parse("2006-01-02 15:04:05.999999999 -0700 MST", properties[1])
	sha1 := properties[2]
	sha256 := properties[3]
	md5 := properties[4]

	rehash = rehash ||
		(GetConfig().Hashes.SHA1 && len(sha1) == 0) ||
		(GetConfig().Hashes.SHA256 && len(sha256) == 0) ||
		(GetConfig().Hashes.MD5 && len(md5) == 0)

	if rehash || size != d.size || !modTime.Equal(d.modTime) {
		if dummyFile {
			d.sha1 = dfData.Sha1
			d.sha256 = dfData.Sha256
			d.md5 = dfData.Md5
		} else {
			h, err := filesystem.HashFile(GetConfig().Repository + d.path)
			if err != nil {
				log.Warningf("%s: hashing failed: %s", d.path, err.Error())
			} else {
				d.sha1 = h.Sha1
				d.sha256 = h.Sha256
				d.md5 = h.Md5
				if len(d.sha1) > 0 {
					log.Infof("%s: SHA1 %s", d.path, d.sha1)
				}
				if len(d.sha256) > 0 {
					log.Infof("%s: SHA256 %s", d.path, d.sha256)
				}
				if len(d.md5) > 0 {
					log.Infof("%s: MD5 %s", d.path, d.md5)
				}
			}
		}
	} else {
		d.sha1 = sha1
		d.sha256 = sha256
		d.md5 = md5
	}

	return d, nil
}

// ScanSource starts a scan of the local repository
func ScanSource(r *database.Redis, forceRehash bool, stop chan bool) (err error) {
	s := &sourcescanner{}
	s.dummyFile = GetConfig().DummyFiles

	conn := r.Get()
	defer conn.Close()

	if conn.Err() != nil {
		return conn.Err()
	}

	sourceFiles := make([]*filedata, 0, 1000)

	//TODO lock atomically inside redis to avoid two simultaneous scan

	if _, err := os.Stat(GetConfig().Repository); os.IsNotExist(err) {
		return fmt.Errorf("%s: No such file or directory", GetConfig().Repository)
	}

	log.Info("[source] Scanning the filesystem...")
	err = filepath.Walk(GetConfig().Repository, func(path string, f os.FileInfo, err error) error {
		fd, err := s.walkSource(conn, path, f, forceRehash, err)
		if err != nil {
			return err
		}
		if fd != nil {
			sourceFiles = append(sourceFiles, fd)
		}
		return nil
	})

	if utils.IsStopped(stop) {
		return ErrScanAborted
	}
	if err != nil {
		return err
	}
	log.Info("[source] Indexing the files...")

	lock := network.NewClusterLock(r, "SOURCE_REPO_SYNC", "source repository")

	retry := 10
	for {
		if retry == 0 {
			return ErrScanInProgress
		}
		done, err := lock.Get()
		if err != nil {
			return err
		} else if done != nil {
			break
		}
		time.Sleep(1 * time.Second)
		retry--
	}

	defer lock.Release()

	conn.Send("MULTI")

	// Remove any left over
	conn.Send("DEL", "FILES_TMP")

	// Add all the files to a temporary key
	count := 0
	for _, e := range sourceFiles {
		conn.Send("SADD", "FILES_TMP", e.path)
		count++
	}

	_, err = conn.Do("EXEC")
	if err != nil {
		return err
	}

	// Do a diff between the sets to get the removed files
	toremove, err := redis.Values(conn.Do("SDIFF", "FILES", "FILES_TMP"))

	// Create/Update the files' hash keys with the fresh infos
	conn.Send("MULTI")
	for _, e := range sourceFiles {
		conn.Send("HMSET", fmt.Sprintf("FILE_%s", e.path),
			"size", e.size,
			"modTime", e.modTime,
			"sha1", e.sha1,
			"sha256", e.sha256,
			"md5", e.md5)

		// Publish update
		database.SendPublish(conn, database.FILE_UPDATE, e.path)
	}

	// Remove old keys
	if len(toremove) > 0 {
		for _, e := range toremove {
			conn.Send("DEL", fmt.Sprintf("FILE_%s", e))

			// Publish update
			database.SendPublish(conn, database.FILE_UPDATE, fmt.Sprintf("%s", e))
		}
	}

	// Finally rename the temporary sets containing the list
	// of files to the production key
	conn.Send("RENAME", "FILES_TMP", "FILES")

	_, err = conn.Do("EXEC")
	if err != nil {
		return err
	}

	log.Infof("[source] Scanned %d files", count)

	return nil
}
