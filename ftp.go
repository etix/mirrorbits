// Copyright (c) 2014-2015 Ludovic Fauvet
// Licensed under the MIT license

package main

import (
	"fmt"
	"github.com/garyburd/redigo/redis"
	"github.com/jlaffaye/ftp"
	"net/url"
	"os"
	"strings"
)

type FTPScanner struct {
	scan *scan
}

func (f *FTPScanner) Scan(scanurl, identifier string, conn redis.Conn, stop chan bool) error {
	if !strings.HasPrefix(scanurl, "ftp://") {
		return fmt.Errorf("%s does not start with ftp://", scanurl)
	}

	ftpurl, err := url.Parse(scanurl)
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
		return fmt.Errorf("ftp error %s", err.Error())
	}

	prefixDir, err := c.CurrentDir()
	if err != nil {
		return fmt.Errorf("ftp error %s", err.Error())
	}
	if os.Getenv("DEBUG") != "" {
		_ = prefixDir
		//fmt.Printf("[%s] Current dir: %s\n", identifier, prefixDir)
	}
	prefix := ftpurl.Path

	// Remove the trailing slash
	prefix = strings.TrimRight(prefix, "/")

	files, err = f.walkFtp(c, files, prefix+"/", stop)
	if err != nil {
		return fmt.Errorf("ftp error %s", err.Error())
	}

	count := 0
	for _, fd := range files {
		fd.path = strings.TrimPrefix(fd.path, prefix)

		if os.Getenv("DEBUG") != "" {
			fmt.Printf("%s\n", fd.path)
		}

		f.scan.ScannerAddFile(*fd)

		count++
	}

	return nil
}

// Walk inside an FTP repository
func (f *FTPScanner) walkFtp(c *ftp.ServerConn, files []*filedata, path string, stop chan bool) ([]*filedata, error) {
	if isStopped(stop) {
		return nil, scanAborted
	}

	flist, err := c.List(path)
	if err != nil {
		return nil, err
	}
	for _, e := range flist {
		if e.Type == ftp.EntryTypeFile {
			newf := &filedata{}
			newf.path = path + e.Name
			newf.size = int64(e.Size)
			files = append(files, newf)
		} else if e.Type == ftp.EntryTypeFolder {
			files, err = f.walkFtp(c, files, path+e.Name+"/", stop)
			if err != nil {
				return files, err
			}
		}
	}
	return files, err
}
