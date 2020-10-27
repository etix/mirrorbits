// Copyright (c) 2014-2020 Ludovic Fauvet
// Licensed under the MIT license

package filesystem

import (
	"bufio"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
	"os"

	. "github.com/etix/mirrorbits/config"
)

// HashFile generates a human readable hash of the given file path
func HashFile(path string) (hashes FileInfo, err error) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	reader := bufio.NewReader(f)

	var writers []io.Writer

	if GetConfig().Hashes.SHA1 {
		hsha1 := newHasher(sha1.New(), &hashes.Sha1)
		defer hsha1.Close()
		writers = append(writers, hsha1)
	}
	if GetConfig().Hashes.SHA256 {
		hsha256 := newHasher(sha256.New(), &hashes.Sha256)
		defer hsha256.Close()
		writers = append(writers, hsha256)
	}
	if GetConfig().Hashes.MD5 {
		hmd5 := newHasher(md5.New(), &hashes.Md5)
		defer hmd5.Close()
		writers = append(writers, hmd5)
	}

	if len(writers) == 0 {
		return
	}

	w := io.MultiWriter(writers...)

	_, err = io.Copy(w, reader)
	if err != nil {
		return
	}

	return
}

type hasher struct {
	hash.Hash
	output *string
}

func newHasher(hash hash.Hash, output *string) hasher {
	return hasher{
		Hash:   hash,
		output: output,
	}
}

func (h hasher) Close() error {
	*h.output = hex.EncodeToString(h.Sum(nil))
	return nil
}

// Sha256sum generates a human readable sha256 hash of the given file path
func Sha256sum(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}

	return h.Sum(nil), nil
}
