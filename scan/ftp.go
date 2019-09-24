// Copyright (c) 2014-2019 Ludovic Fauvet
// Licensed under the MIT license

package scan

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	ftp "github.com/etix/goftp"
	"github.com/etix/mirrorbits/utils"
	"github.com/gomodule/redigo/redis"
)

const (
	ftpConnTimeout = 5 * time.Second
	ftpRWTimeout   = 30 * time.Second
)

// FTPScanner is the implementation of an ftp scanner
type FTPScanner struct {
	scan *scan

	featMLST bool
	featMDTM bool
}

// Scan starts an ftp scan of the given mirror
func (f *FTPScanner) Scan(scanurl, identifier string, conn redis.Conn, stop <-chan struct{}) error {
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

	if utils.IsStopped(stop) {
		return ErrScanAborted
	}

	c, err := ftp.DialTimeout(host, ftpConnTimeout, ftpRWTimeout)
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

	_, f.featMLST = c.Feature("MLST")
	_, f.featMDTM = c.Feature("MDTM")

	if !f.featMLST || !f.featMDTM {
		log.Warning("This server does not support some of the RFC 3659 extensions, consider using rsync instead.")
	}

	log.Infof("[%s] Requesting file list via ftp...", identifier)

	files := make([]*filedata, 0, 1000)

	err = c.ChangeDir(ftpurl.Path)
	if err != nil {
		return fmt.Errorf("ftp error %s", err.Error())
	}

	_, err = c.CurrentDir()
	if err != nil {
		return fmt.Errorf("ftp error %s", err.Error())
	}

	// Remove the trailing slash
	prefix := strings.TrimRight(ftpurl.Path, "/")

	files, err = f.walkFtp(c, files, prefix+"/", stop)
	if err != nil {
		return fmt.Errorf("ftp error %s", err.Error())
	}

	count := 0
	for _, fd := range files {
		fd.path = strings.TrimPrefix(fd.path, prefix)
		f.scan.ScannerAddFile(*fd)
		count++
	}

	return nil
}

// Walk inside an FTP repository
func (f *FTPScanner) walkFtp(c *ftp.ServerConn, files []*filedata, path string, stop <-chan struct{}) ([]*filedata, error) {
	if utils.IsStopped(stop) {
		return nil, ErrScanAborted
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

			if f.featMDTM {
				t, _ := c.LastModificationDate(path + e.Name)
				if !t.IsZero() {
					newf.modTime = t
				}
			}
			if newf.modTime.IsZero() {
				if f.featMLST {
					newf.modTime = e.Time
				} else {
					newf.modTime = time.Time{}
				}
			}
			files = append(files, newf)
		} else if e.Type == ftp.EntryTypeFolder {
			if e.Name == "." || e.Name == ".." {
				continue
			}
			files, err = f.walkFtp(c, files, path+e.Name+"/", stop)
			if err != nil {
				return files, err
			}
		}
	}
	return files, err
}
