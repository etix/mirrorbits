// Copyright (c) 2014-2017 Ludovic Fauvet
// Licensed under the MIT license

package filesystem

import (
	"time"
)

// FileInfo is a struct embedding details about a file served by
// the redirector.
type FileInfo struct {
	Path    string    `redis:"-"`
	Size    int64     `redis:"size" json:",omitempty"`
	ModTime time.Time `redis:"modTime" json:",omitempty"`
	Sha1    string    `redis:"sha1" json:",omitempty"`
	Sha256  string    `redis:"sha256" json:",omitempty"`
	Md5     string    `redis:"md5" json:",omitempty"`
}

// NewFileInfo returns a new FileInfo object
func NewFileInfo(path string) FileInfo {
	return FileInfo{
		Path: path,
	}
}
