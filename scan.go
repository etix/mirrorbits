// Copyright (c) 2014 Ludovic Fauvet
// Licensed under the MIT license

package main

import (
	"bufio"
	"errors"
	"fmt"
	"github.com/garyburd/redigo/redis"
	"github.com/jlaffaye/ftp"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	scanAborted     = errors.New("scan aborted")
	rsyncOutputLine = regexp.MustCompile(`^.+\s+([0-9,]+)\s+([0-9/]+)+\s+([0-9:]+)\s+(.*)$`)
)

type filedata struct {
	path    string
	sha1    string
	size    int64
	modTime time.Time
}

type scan struct {
	redis           *redisobj
	walkSourceFiles []*filedata
	walkRedisConn   redis.Conn
}

func Scan(r *redisobj) *scan {
	return &scan{
		redis: r,
	}
}

// Scan an rsync repository and index its files
func (s *scan) ScanRsync(url, identifier string, stop chan bool) (err error) {
	if !strings.HasPrefix(url, "rsync://") {
		log.Warning("[%s] %s does not start with rsync://", identifier, url)
		return
	}

	// Always ensures there's a trailing slash
	if url[len(url)-1] != '/' {
		url = url + "/"
	}

	cmd := exec.Command("rsync", "-r", "--no-motd", "--timeout=30", "--contimeout=30", url)
	stdout, err := cmd.StdoutPipe()

	if err != nil {
		return err
	}

	// Connect to the database
	conn := s.redis.pool.Get()
	defer conn.Close()

	// Pipe stdout
	r := bufio.NewReader(stdout)

	if isStopped(stop) {
		return scanAborted
	}

	// Start the process
	if err := cmd.Start(); err != nil {
		return err
	}

	log.Info("[%s] Requesting file list via rsync...", identifier)

	// Get the list of all source files (we do not want to
	// index files than are not provided by the source)
	//sourceFiles, err := redis.Values(conn.Do("SMEMBERS", "FILES"))
	//if err != nil {
	//	log.Error("[%s] Cannot get the list of source files", identifier)
	//	return err
	//}

	conn.Send("MULTI")

	filesKey := fmt.Sprintf("MIRROR_%s_FILES", identifier)
	filesTmpKey := fmt.Sprintf("MIRROR_%s_FILES_TMP", identifier)

	// Remove any left over
	conn.Send("DEL", filesTmpKey)

	count := 0

	line, err := readln(r)
	for err == nil {
		var size int64
		var f filedata
		var rk, ik string

		if isStopped(stop) {
			return scanAborted
		}

		// Parse one line returned by rsync
		ret := rsyncOutputLine.FindStringSubmatch(line)
		if ret[0][0] == 'd' || ret[0][0] == 'l' {
			// Skip directories and links
			goto cont
		}

		// Add the leading slash
		if ret[4][0] != '/' {
			ret[4] = "/" + ret[4]
		}

		// Remove the commas in the file size
		ret[1] = strings.Replace(ret[1], ",", "", -1)
		// Convert the size to int
		size, err = strconv.ParseInt(ret[1], 10, 64)
		if err != nil {
			log.Error("[%s] ScanRsync: Invalid size: %s", identifier, ret[1])
			goto cont
		}

		// Fill the struct
		f.size = size
		f.path = ret[4]

		if os.Getenv("DEBUG") != "" {
			//fmt.Printf("[%s] %s", identifier, f.path)
		}

		// Add all the files to a temporary key
		conn.Send("SADD", filesTmpKey, f.path)

		// Mark the file as being supported by this mirror
		rk = fmt.Sprintf("FILEMIRRORS_%s", f.path)
		conn.Send("SADD", rk, identifier)

		// Save the size of the current file found on this mirror
		ik = fmt.Sprintf("FILEINFO_%s_%s", identifier, f.path)
		conn.Send("HSET", ik, "size", f.size)

		// Publish update
		conn.Send("PUBLISH", MIRROR_FILE_UPDATE, fmt.Sprintf("%s %s", identifier, f.path))

		count++
	cont:
		line, err = readln(r)
	}

	if err1 := cmd.Wait(); err1 != nil {
		// Discard MULTI
		conn.Do("DISCARD")

		switch err1.Error() {
		case "exit status 5":
			err1 = errors.New("rsync: Error starting client-server protocol")
			break
		case "exit status 10":
			err1 = errors.New("rsync: Error in socket I/O")
			break
		case "exit status 11":
			err1 = errors.New("rsync: Error in file I/O")
			break
		case "exit status 30":
			err1 = errors.New("rsync: Timeout in data send/receive")
			break
		default:
			err1 = errors.New("rsync: " + err1.Error())
		}
		return err1
	}

	if err == io.EOF {
		_, err1 := conn.Do("EXEC")
		if err1 != nil {
			return err1
		}
	}

	// Get the list of files no more present on this mirror
	toremove, err1 := redis.Values(conn.Do("SDIFF", filesKey, filesTmpKey))
	if err1 != nil {
		return err1
	}

	// Remove this mirror from the given file SET
	if len(toremove) > 0 {
		conn.Send("MULTI")
		for _, e := range toremove {
			log.Info("[%s] Removing %s from mirror", identifier, e)
			conn.Send("SREM", fmt.Sprintf("FILEMIRRORS_%s", e), identifier)
			conn.Send("DEL", fmt.Sprintf("FILEINFO_%s_%s", identifier, e))
			// Publish update
			conn.Send("PUBLISH", MIRROR_FILE_UPDATE, fmt.Sprintf("%s %s", identifier, e))
		}
		_, err1 = conn.Do("EXEC")
		if err1 != nil {
			return err1
		}
	}

	// Finally rename the temporary sets containing the list
	// of files for this mirror to the production key
	_, err1 = conn.Do("RENAME", filesTmpKey, filesKey)
	if err1 != nil {
		return err1
	}

	sinterKey := fmt.Sprintf("HANDLEDFILES_%s", identifier)

	// Count the number of files known on the remote end
	common, _ := redis.Int64(conn.Do("SINTERSTORE", sinterKey, "FILES", filesKey))

	if err == io.EOF {
		s.setLastSync(conn, identifier)
		log.Info("[%s] Indexed %d files (%d known)", identifier, count, common)
		return nil
	}
	return err
}

func readln(r *bufio.Reader) (string, error) {
	var (
		isPrefix bool = true
		err      error
		line, ln []byte
	)
	for isPrefix && err == nil {
		line, isPrefix, err = r.ReadLine()
		ln = append(ln, line...)
	}
	return string(ln), err
}

// Scan an FTP repository and index its files
func (s *scan) ScanFTP(ftpURL, identifier string, stop chan bool) (err error) {
	if !strings.HasPrefix(ftpURL, "ftp://") {
		log.Error("%s does not start with ftp://", ftpURL)
		return
	}

	ftpurl, err := url.Parse(ftpURL)
	if err != nil {
		return err
	}

	host := ftpurl.Host
	if !strings.Contains(host, ":") {
		host += ":21"
	}

	if isStopped(stop) {
		return scanAborted
	}

	c, err := ftp.Connect(host)
	if err != nil {
		return err
	}
	defer c.Quit()

	username, password := "anonymous", "anonymous"

	if ftpurl.User != nil {
		username = ftpurl.User.Username()
		pass, hasPassword := ftpurl.User.Password()
		if hasPassword {
			password = pass
		}
	}

	err = c.Login(username, password)
	if err != nil {
		return err
	}

	log.Info("[%s] Requesting file list via ftp...", identifier)

	var files []*filedata = make([]*filedata, 0, 1000)

	err = c.ChangeDir(ftpurl.Path)
	if err != nil {
		return fmt.Errorf("[%s] ftp error %s", identifier, err.Error())
	}

	prefixDir, err := c.CurrentDir()
	if err != nil {
		return fmt.Errorf("[%s] ftp error %s", identifier, err.Error())
	}
	if os.Getenv("DEBUG") != "" {
		_ = prefixDir
		//fmt.Printf("[%s] Current dir: %s\n", identifier, prefixDir)
	}
	prefix := ftpurl.Path

	// Remove the trailing slash
	prefix = strings.TrimRight(prefix, "/")

	files, err = s.walkFtp(c, files, prefix+"/", stop)
	if err != nil {
		return fmt.Errorf("[%s] ftp error %s", identifier, err.Error())
	}

	// Connect to the database
	conn := s.redis.pool.Get()
	defer conn.Close()

	conn.Send("MULTI")

	filesKey := fmt.Sprintf("MIRROR_%s_FILES", identifier)
	filesTmpKey := fmt.Sprintf("MIRROR_%s_FILES_TMP", identifier)

	// Remove any left over
	conn.Send("DEL", filesTmpKey)

	count := 0
	for _, f := range files {
		f.path = strings.TrimPrefix(f.path, prefix)

		if os.Getenv("DEBUG") != "" {
			fmt.Printf("%s\n", f.path)
		}

		// Add all the files to a temporary key
		conn.Send("SADD", filesTmpKey, f.path)

		// Mark the file as being supported by this mirror
		rk := fmt.Sprintf("FILEMIRRORS_%s", f.path)
		conn.Send("SADD", rk, identifier)

		// Save the size of the current file found on this mirror
		ik := fmt.Sprintf("FILEINFO_%s_%s", identifier, f.path)
		conn.Send("HSET", ik, "size", f.size)

		// Publish update
		conn.Send("PUBLISH", MIRROR_FILE_UPDATE, fmt.Sprintf("%s %s", identifier, f.path))

		count++
	}

	_, err = conn.Do("EXEC")
	if err != nil {
		return err
	}

	// Get the list of files no more present on this mirror
	toremove, err := redis.Values(conn.Do("SDIFF", filesKey, filesTmpKey))
	if err != nil {
		return err
	}

	// Remove this mirror from the given file SET
	if len(toremove) > 0 {
		conn.Send("MULTI")
		for _, e := range toremove {
			log.Info("[%s] Removing %s from mirror", identifier, e)
			conn.Send("SREM", fmt.Sprintf("FILEMIRRORS_%s", e), identifier)
			conn.Send("DEL", fmt.Sprintf("FILEINFO_%s_%s", identifier, e))
			conn.Send("PUBLISH", MIRROR_FILE_UPDATE, fmt.Sprintf("%s %s", identifier, e))
		}
		_, err = conn.Do("EXEC")
		if err != nil {
			return err
		}
	}

	// Finally rename the temporary sets containing the list
	// of files for this mirror to the production key
	if count > 0 {
		_, err = conn.Do("RENAME", filesTmpKey, filesKey)
		if err != nil {
			return err
		}
	} else {
		_, _ = conn.Do("DEL", filesKey)
	}

	sinterKey := fmt.Sprintf("HANDLEDFILES_%s", identifier)

	// Count the number of files known on the remote end
	common, _ := redis.Int64(conn.Do("SINTERSTORE", sinterKey, "FILES", filesKey))

	s.setLastSync(conn, identifier)
	log.Info("[%s] Indexed %d files (%d known)", identifier, count, common)

	return nil
}

// Walk inside an FTP repository
func (s *scan) walkFtp(c *ftp.ServerConn, files []*filedata, path string, stop chan bool) ([]*filedata, error) {
	if isStopped(stop) {
		return nil, scanAborted
	}

	f, err := c.List(path)
	if err != nil {
		return nil, err
	}
	for _, e := range f {
		if e.Type == ftp.EntryTypeFile {
			newf := &filedata{}
			newf.path = path + e.Name
			newf.size = int64(e.Size)
			files = append(files, newf)
		} else if e.Type == ftp.EntryTypeFolder {
			files, err = s.walkFtp(c, files, path+e.Name+"/", stop)
			if err != nil {
				return files, err
			}
		}
	}
	return files, err
}

func (s *scan) setLastSync(conn redis.Conn, identifier string) error {
	// Set the last sync time
	_, err := conn.Do("HSET", fmt.Sprintf("MIRROR_%s", identifier), "lastSync", time.Now().UTC().Unix())
	// Publish an update on redis
	conn.Do("PUBLISH", MIRROR_UPDATE, identifier)
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
	properties, err := redis.Strings(s.walkRedisConn.Do("HMGET", fmt.Sprintf("FILE_%s", d.path), "size", "modTime", "sha1"))
	if err != nil && err != redis.ErrNil {
		return err
	}

	if err != redis.ErrNil && len(properties) >= 3 {
		size, _ := strconv.ParseInt(properties[0], 10, 64)
		modTime, _ := time.Parse("2006-01-02 15:04:05.999999999 -0700 MST", properties[1])
		sha1 := properties[2]
		if size != d.size || modTime != d.modTime {
			h, err := hashFile(GetConfig().Repository + d.path)
			if err != nil {
				log.Warning("%s: hashing failed: %s", d.path, err.Error())
			} else {
				log.Info("%s: %s", d.path, h)
				d.sha1 = h
			}
		}
		if d.sha1 == "" {
			d.sha1 = sha1
		}
	}

	s.walkSourceFiles = append(s.walkSourceFiles, d)
	return nil
}

func (s *scan) ScanSource(stop chan bool) (err error) {
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
		return fmt.Errorf("[source] Error during walking: %s", err.Error())
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
			"sha1", e.sha1)

		// Publish update
		s.walkRedisConn.Send("PUBLISH", FILE_UPDATE, e.path)
	}

	// Remove old keys
	if len(toremove) > 0 {
		for _, e := range toremove {
			s.walkRedisConn.Send("DEL", fmt.Sprintf("FILE_%s", e))

			// Publish update
			s.walkRedisConn.Send("PUBLISH", FILE_UPDATE, e)
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
