// Copyright (c) 2014-2019 Ludovic Fauvet
// Licensed under the MIT license

package http

import (
	"net/http"
	"net/url"
	"strings"

	. "github.com/etix/mirrorbits/config"
)

// RequestType defines the type of the request
type RequestType int

// SecureOption is the type that defines TLS requirements
type SecureOption int

const (
	STANDARD RequestType = iota
	MIRRORLIST
	FILESTATS
	MIRRORSTATS
	CHECKSUM

	UNDEFINED SecureOption = iota
	WITHTLS
	WITHOUTTLS
)

// Context represents the context of a request
type Context struct {
	r             *http.Request
	w             http.ResponseWriter
	t             Templates
	v             url.Values
	typ           RequestType
	isMirrorList  bool
	isMirrorStats bool
	isFileStats   bool
	isChecksum    bool
	isPretty      bool
	secureOption  SecureOption
}

// NewContext returns a new instance of Context
func NewContext(w http.ResponseWriter, r *http.Request, t Templates) *Context {
	c := &Context{r: r, w: w, t: t, v: r.URL.Query()}

	if c.paramBool("mirrorlist") {
		c.typ = MIRRORLIST
		c.isMirrorList = true
	} else if c.paramBool("stats") {
		c.typ = FILESTATS
		c.isFileStats = true
	} else if c.paramBool("mirrorstats") {
		c.typ = MIRRORSTATS
		c.isMirrorStats = true
	} else if c.paramBool("md5") || c.paramBool("sha1") || c.paramBool("sha256") {
		c.typ = CHECKSUM
		c.isChecksum = true
	} else {
		c.typ = STANDARD
	}

	if c.paramBool("pretty") {
		c.isPretty = true
	}

	// Check for HTTPS requirements
	proto := strings.ToLower(r.Header.Get("X-Forwarded-Proto"))
	if proto == "https" {
		c.secureOption = WITHTLS
	} else if proto == "http" && GetConfig().AllowHTTPToHTTPSRedirects == false {
		c.secureOption = WITHOUTTLS
	}

	// Check if the query sets (thus overrides) HTTPS requirements
	v, ok := c.v["https"]
	if ok {
		if v[0] == "1" {
			c.secureOption = WITHTLS
		} else if v[0] == "0" {
			c.secureOption = WITHOUTTLS
		}
	}

	return c
}

// Request returns the underlying http.Request of the current request
func (c *Context) Request() *http.Request {
	return c.r
}

// ResponseWriter returns the underlying http.ResponseWriter of the current request
func (c *Context) ResponseWriter() http.ResponseWriter {
	return c.w
}

// Templates returns the instance of precompiled templates
func (c *Context) Templates() Templates {
	return c.t
}

// Type returns the type of the current request
func (c *Context) Type() RequestType {
	return c.typ
}

// IsMirrorlist returns true if the mirror list has been requested
func (c *Context) IsMirrorlist() bool {
	return c.isMirrorList
}

// IsFileStats returns true if the file stats has been requested
func (c *Context) IsFileStats() bool {
	return c.isFileStats
}

// IsMirrorStats returns true if the mirror stats has been requested
func (c *Context) IsMirrorStats() bool {
	return c.isMirrorStats
}

// IsChecksum returns true if a checksum has been requested
func (c *Context) IsChecksum() bool {
	return c.isChecksum
}

// IsPretty returns true if the pretty json has been requested
func (c *Context) IsPretty() bool {
	return c.isPretty
}

// QueryParam returns the value associated with the given query parameter
func (c *Context) QueryParam(key string) string {
	return c.v.Get(key)
}

// SecureOption returns the selected secure option
func (c *Context) SecureOption() SecureOption {
	return c.secureOption
}

func (c *Context) paramBool(key string) bool {
	_, ok := c.v[key]
	return ok
}
