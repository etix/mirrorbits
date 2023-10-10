// Copyright (c) 2014-2019 Ludovic Fauvet
// Licensed under the MIT license

package scan

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	. "github.com/etix/mirrorbits/config"
	"github.com/etix/mirrorbits/core"
	"github.com/etix/mirrorbits/database"
	"github.com/etix/mirrorbits/filesystem"
	"github.com/etix/mirrorbits/mirrors"
	"github.com/etix/mirrorbits/network"
	"github.com/etix/mirrorbits/utils"
	"github.com/gomodule/redigo/redis"
	"github.com/op/go-logging"
)

var (
	// ErrScanAborted is returned when a scan is aborted by the user
	ErrScanAborted = errors.New("scan aborted")
	// ErrScanInProgress is returned when a scan is started while another is already in progress
	ErrScanInProgress = errors.New("scan already in progress")
	// ErrNoSyncMethod is returned when no sync protocol is available
	ErrNoSyncMethod = errors.New("no suitable URL for the scan")

	log = logging.MustGetLogger("main")
)

// Scanner is the interface that all scanners must implement
type Scanner interface {
	Scan(url, identifier string, conn redis.Conn, stop <-chan struct{}) (core.Precision, error)
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
	cache *mirrors.Cache

	conn        redis.Conn
	mirrorid    int
	filesTmpKey string
	files       []filedata
	count       int64
}

type ScanResult struct {
	MirrorID     int
	MirrorName   string
	FilesIndexed int64
	KnownIndexed int64
	Removed      int64
	TZOffsetMs   int64
}

// IsScanning returns true is a scan is already in progress for the given mirror
func IsScanning(conn redis.Conn, id int) (bool, error) {
	return redis.Bool(conn.Do("EXISTS", fmt.Sprintf("SCANNING_%d", id)))
}

// Scan starts a scan of the given mirror
func Scan(typ core.ScannerType, r *database.Redis, c *mirrors.Cache, url string, id int, stop <-chan struct{}) (*ScanResult, error) {
	// Connect to the database
	conn := r.Get()
	defer conn.Close()

	s := &scan{
		redis:    r,
		mirrorid: id,
		conn:     conn,
		cache:    c,
		files:    make([]filedata, 0, 1000),
	}

	var scanner Scanner
	switch typ {
	case core.RSYNC:
		scanner = &RsyncScanner{
			scan: s,
		}
	case core.FTP:
		scanner = &FTPScanner{
			scan: s,
		}
	default:
		panic(fmt.Sprintf("Unknown scanner"))
	}

	// Get the mirror name
	name, err := redis.String(conn.Do("HGET", "MIRRORS", id))
	if err != nil {
		return nil, err
	}

	// Try to acquire a lock so we don't have a scanning race
	// from different nodes.
	// Also make the key expire automatically in case our process
	// gets killed.
	lock := network.NewClusterLock(s.redis, fmt.Sprintf("SCANNING_%d", id), name)

	done, err := lock.Get()
	if err != nil {
		return nil, err
	} else if done == nil {
		return nil, ErrScanInProgress
	}

	defer lock.Release()

	s.setLastSync(conn, id, typ, 0, false)

	mirrors.PushLog(r, mirrors.NewLogScanStarted(id, typ))
	defer func(err *error) {
		if err != nil && *err != nil {
			mirrors.PushLog(r, mirrors.NewLogError(id, *err))
		}
	}(&err)

	filesKey := fmt.Sprintf("MIRRORFILES_%d", id)
	s.filesTmpKey = fmt.Sprintf("MIRRORFILESTMP_%d", id)

	// Remove any left over
	_, err = conn.Do("DEL", s.filesTmpKey)
	if err != nil {
		return nil, err
	}

	// Scan the mirror
	var precision core.Precision
	precision, err = scanner.Scan(url, name, conn, stop)
	if err != nil {
		log.Errorf("[%s] %s", name, err.Error())
		return nil, err
	}

	// Commit changes
	s.ScannerCommit()

	// Get the list of files no more present on this mirror
	var toremove []interface{}
	toremove, err = redis.Values(conn.Do("SDIFF", filesKey, s.filesTmpKey))
	if err != nil {
		return nil, err
	}

	// Remove this mirror from the given file SET
	if len(toremove) > 0 {
		conn.Send("MULTI")
		for _, e := range toremove {
			log.Debugf("[%s] Removing %s from mirror", name, e)
			conn.Send("SREM", fmt.Sprintf("FILEMIRRORS_%s", e), id)
			conn.Send("DEL", fmt.Sprintf("FILEINFO_%d_%s", id, e))
			// Publish update
			database.SendPublish(conn, database.MIRROR_FILE_UPDATE, fmt.Sprintf("%d %s", id, e))

		}
		_, err = conn.Do("EXEC")
		if err != nil {
			return nil, err
		}
	}

	// Finally rename the temporary sets containing the list
	// of files for this mirror to the production key
	if s.count > 0 {
		_, err = conn.Do("RENAME", s.filesTmpKey, filesKey)
		if err != nil {
			return nil, err
		}
	}

	sinterKey := fmt.Sprintf("HANDLEDFILES_%d", id)

	// Count the number of files known on the remote end
	common, _ := redis.Int64(conn.Do("SINTERSTORE", sinterKey, "FILES", filesKey))

	if err != nil {
		return nil, err
	}

	s.setLastSync(conn, id, typ, precision, true)

	var tzoffset int64
	tzoffset, err = s.adjustTZOffset(name, precision)
	if err != nil {
		log.Warningf("Unable to check timezone shifts: %s", err)
	}

	log.Infof("[%s] Indexed %d files (%d known), %d removed", name, s.count, common, len(toremove))
	res := &ScanResult{
		MirrorID:     id,
		MirrorName:   name,
		FilesIndexed: s.count,
		KnownIndexed: common,
		Removed:      int64(len(toremove)),
		TZOffsetMs:   tzoffset,
	}

	mirrors.PushLog(r, mirrors.NewLogScanCompleted(
		res.MirrorID,
		res.FilesIndexed,
		res.KnownIndexed,
		res.Removed,
		res.TZOffsetMs))

	return res, nil
}

func (s *scan) ScannerAddFile(f filedata) {
	s.count++
	s.files = append(s.files, f)
}

func (s *scan) ScannerCommit() error {
	s.conn.Send("MULTI")

	for _, f := range s.files {
		// Add all the files to a temporary key
		s.conn.Send("SADD", s.filesTmpKey, f.path)

		// Mark the file as being supported by this mirror
		rk := fmt.Sprintf("FILEMIRRORS_%s", f.path)
		s.conn.Send("SADD", rk, s.mirrorid)

		// Save the size of the current file found on this mirror
		ik := fmt.Sprintf("FILEINFO_%d_%s", s.mirrorid, f.path)
		s.conn.Send("HMSET", ik, "size", f.size, "modTime", f.modTime)

		// Publish update
		database.SendPublish(s.conn, database.MIRROR_FILE_UPDATE, fmt.Sprintf("%d %s", s.mirrorid, f.path))
	}

	_, err := s.conn.Do("EXEC")

	return err
}

func (s *scan) setLastSync(conn redis.Conn, id int, protocol core.ScannerType, precision core.Precision, successful bool) error {
	now := time.Now().UTC().Unix()

	conn.Send("MULTI")

	// Set the last sync time
	conn.Send("HSET", fmt.Sprintf("MIRROR_%d", id), "lastSync", now)

	// Set the last successful sync time
	if successful {
		if precision == 0 {
			precision = core.Precision(time.Second)
		}

		conn.Send("HMSET", fmt.Sprintf("MIRROR_%d", id),
			"lastSuccessfulSync", now,
			"lastSuccessfulSyncProtocol", protocol,
			"lastSuccessfulSyncPrecision", precision)
	}

	_, err := conn.Do("EXEC")

	// Publish an update on redis
	database.Publish(conn, database.MIRROR_UPDATE, strconv.Itoa(id))

	return err
}

func (s *scan) adjustTZOffset(name string, precision core.Precision) (ms int64, err error) {
	type pair struct {
		local  filesystem.FileInfo
		remote filesystem.FileInfo
	}

	var filepaths []string
	var pairs []pair
	var offsetmap map[int64]int
	var commonOffsetFound bool

	if s.cache == nil {
		log.Error("Skipping timezone check: missing cache in instance")
		return
	}

	if GetConfig().FixTimezoneOffsets == false {
		// We need to reset any previous value already
		// stored in the database.
		goto finish
	}

	// Get 100 random files from the mirror
	filepaths, err = redis.Strings(s.conn.Do("SRANDMEMBER", fmt.Sprintf("HANDLEDFILES_%d", s.mirrorid), 100))
	if err != nil {
		return
	}

	pairs = make([]pair, 0, 100)

	// Get the metadata of each file
	for _, path := range filepaths {
		p := pair{}

		p.local, err = s.cache.GetFileInfo(path)
		if err != nil {
			return
		}

		p.remote, err = s.cache.GetFileInfoMirror(s.mirrorid, path)
		if err != nil {
			return
		}

		if p.remote.ModTime.IsZero() {
			// Invalid mod time
			continue
		}

		if p.local.Size != p.remote.Size {
			// File differ: comparing the modfile will fail
			continue
		}

		// Add the file to valid pairs
		pairs = append(pairs, p)
	}

	if len(pairs) < 10 || len(pairs) < len(filepaths)/2 {
		// Less than half the files we got have a size
		// match, this is very suspicious. Skip the
		// check and reset the offset in the db.
		goto warn
	}

	// Compute the diff between local and remote for those files
	offsetmap = make(map[int64]int)
	for _, p := range pairs {
		// Convert to millisecond since unix timestamp truncating to the available precision
		local := p.local.ModTime.Truncate(precision.Duration()).UnixNano() / int64(time.Millisecond)
		remote := p.remote.ModTime.Truncate(precision.Duration()).UnixNano() / int64(time.Millisecond)

		diff := local - remote
		offsetmap[diff]++
	}

	for k, v := range offsetmap {
		// Find the common offset (if any) of at least 90% of our subset
		if v >= int(float64(len(pairs))/100*90) {
			ms = k
			commonOffsetFound = true
			break
		}
	}

warn:
	if !commonOffsetFound {
		log.Warningf("[%s] Unable to guess the timezone offset", name)
	}

finish:
	// Store the offset in the database
	key := fmt.Sprintf("MIRROR_%d", s.mirrorid)
	_, err = s.conn.Do("HMSET", key, "tzoffset", ms)
	if err != nil {
		return
	}

	// Publish update
	database.Publish(s.conn, database.MIRROR_UPDATE, strconv.Itoa(s.mirrorid))

	if ms != 0 {
		log.Noticef("[%s] Timezone offset detected: applied correction of %dms", name, ms)
	}

	return
}

type sourcescanner struct {
}

// Walk inside the source/reference repository
func (s *sourcescanner) walkSource(conn redis.Conn, path string, f os.FileInfo, rehash bool, err error) (*filedata, error) {
	if f == nil || f.IsDir() || f.Mode()&os.ModeSymlink != 0 {
		return nil, nil
	}

	d := new(filedata)
	d.path = path[len(GetConfig().Repository):]
	d.size = f.Size()
	d.modTime = f.ModTime()

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
	} else {
		d.sha1 = sha1
		d.sha256 = sha256
		d.md5 = md5
	}

	return d, nil
}

// ScanSource starts a scan of the local repository
func ScanSource(r *database.Redis, forceRehash bool, stop <-chan struct{}) (err error) {
	s := &sourcescanner{}

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

	// Remove any left over
	_, err = conn.Do("DEL", "FILES_TMP")
	if err != nil {
		return err
	}

	// Add all the files to a temporary key
	conn.Send("MULTI")
	for count, e := range sourceFiles {
		conn.Send("SADD", "FILES_TMP", e.path)

		if count > 0 && count % database.RedisMultiMaxSize == 0 {
			_, err := conn.Do("EXEC")
			if err != nil {
				return err
			}
			conn.Send("MULTI")
		}
	}

	_, err = conn.Do("EXEC")
	if err != nil {
		return err
	}

	// Do a diff between the sets to get the removed files
	toremove, err := redis.Values(conn.Do("SDIFF", "FILES", "FILES_TMP"))
	if err != nil {
		return err
	}

	// Create/Update the files' hash keys with the fresh infos
	conn.Send("MULTI")
	for count, e := range sourceFiles {
		conn.Send("HMSET", fmt.Sprintf("FILE_%s", e.path),
			"size", e.size,
			"modTime", e.modTime,
			"sha1", e.sha1,
			"sha256", e.sha256,
			"md5", e.md5)

		// Publish update
		database.SendPublish(conn, database.FILE_UPDATE, e.path)

		if count > 0 && count % database.RedisMultiMaxSize == 0 {
			_, err := conn.Do("EXEC")
			if err != nil {
				return err
			}
			conn.Send("MULTI")
		}
	}

	_, err = conn.Do("EXEC")
	if err != nil {
		return err
	}

	// Remove old keys
	if len(toremove) > 0 {
		conn.Send("MULTI")
		for count, e := range toremove {
			conn.Send("DEL", fmt.Sprintf("FILE_%s", e))

			// Publish update
			database.SendPublish(conn, database.FILE_UPDATE, fmt.Sprintf("%s", e))

			if count > 0 && count % database.RedisMultiMaxSize == 0 {
				_, err = conn.Do("EXEC")
				if err != nil {
					return err
				}
				conn.Send("MULTI")
			}
		}
		_, err = conn.Do("EXEC")
		if err != nil {
			return err
		}
	}

	// Finally rename the temporary sets containing the list
	// of files to the production key
	_, err = conn.Do("RENAME", "FILES_TMP", "FILES")
	if err != nil {
		return err
	}

	log.Infof("[source] Scanned %d files", len(sourceFiles))

	return nil
}
