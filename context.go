// Copyright (c) 2014 Ludovic Fauvet
// Licensed under the MIT license

package main

import (
	"html/template"
	"net/http"
	"net/url"
)

type RequestType int

const (
	STANDARD RequestType = iota
	MIRRORLIST
	FILESTATS
	MIRRORSTATS
)

type Context struct {
	r             *http.Request
	w             http.ResponseWriter
	t             *template.Template
	v             url.Values
	typ           RequestType
	isMirrorList  bool
	isMirrorStats bool
	isFileStats   bool
	isPretty      bool
}

func NewContext(w http.ResponseWriter, r *http.Request, t *template.Template) *Context {
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

func (c *Context) Request() *http.Request {
	return c.r
}

func (c *Context) ResponseWriter() http.ResponseWriter {
	return c.w
}

func (c *Context) Templates() *template.Template {
	return c.t
}

func (c *Context) Type() RequestType {
	return c.typ
}

func (c *Context) IsMirrorlist() bool {
	return c.isMirrorList
}

func (c *Context) IsFileStats() bool {
	return c.isFileStats
}

func (c *Context) IsMirrorStats() bool {
	return c.isMirrorStats
}

func (c *Context) IsPretty() bool {
	return c.isPretty
}

func (c *Context) QueryParam(key string) string {
	return c.v.Get(key)
}

func (c *Context) paramBool(key string) bool {
	_, ok := c.v[key]
	return ok
}
