// Copyright (c) 2014 Ludovic Fauvet
// Licensed under the MIT license

package main

import (
	"net/http"
	"net/url"
)

// RequestType defines the type of the request
type RequestType int

const (
	STANDARD RequestType = iota
	MIRRORLIST
	FILESTATS
	MIRRORSTATS
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
	isPretty      bool
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
	} else {
		c.typ = STANDARD
	}

	if c.paramBool("pretty") {
		c.isPretty = true
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

// IsPretty returns true if the pretty json has been requested
func (c *Context) IsPretty() bool {
	return c.isPretty
}

// QueryParam returns the value associated with the given query parameter
func (c *Context) QueryParam(key string) string {
	return c.v.Get(key)
}

func (c *Context) paramBool(key string) bool {
	_, ok := c.v[key]
	return ok
}
