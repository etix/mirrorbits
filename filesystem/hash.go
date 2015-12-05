// Copyright (c) 2014-2015 Ludovic Fauvet
// Licensed under the MIT license

package filesystem

import (
	"bufio"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	. "github.com/etix/mirrorbits/config"
	"io"
	"os"
)

// Generate a human readable sha1 hash of the given file path
func HashFile(path string) (hashes FileInfo, err error) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	reader := bufio.NewReader(f)

	if GetConfig().Hashes.SHA1 {
		sha1Hash := sha1.New()
		_, err = io.Copy(sha1Hash, reader)
		if err == nil {
			hashes.Sha1 = hex.EncodeToString(sha1Hash.Sum(nil))
		}
	}
	if GetConfig().Hashes.SHA256 {
		sha256Hash := sha256.New()
		_, err = io.Copy(sha256Hash, reader)
		if err == nil {
			hashes.Sha256 = hex.EncodeToString(sha256Hash.Sum(nil))
		}
	}
	if GetConfig().Hashes.MD5 {
		md5Hash := md5.New()
		_, err = io.Copy(md5Hash, reader)
		if err == nil {
			hashes.Md5 = hex.EncodeToString(md5Hash.Sum(nil))
		}
	}
	return
}
