// Copyright (c) 2014 Ludovic Fauvet
// Licensed under the MIT license

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

var (
	TemplatesNotFound = errors.New("Please set a valid path to the templates directory.")
)

type PageRenderer interface {
	Write(ctx *Context, page *MirrorlistPage) (int, error)
	Type() string
}

type JsonRenderer struct{}

func (w *JsonRenderer) Type() string {
	return "JSON"
}

func (w *JsonRenderer) Write(ctx *Context, page *MirrorlistPage) (statusCode int, err error) {
	var output []byte
	if ctx.IsPretty() {
		output, err = json.MarshalIndent(page, "", "    ")
	} else {
		output, err = json.Marshal(page)
	}
	if err != nil {
		return http.StatusInternalServerError, err
	}
	ctx.ResponseWriter().Header().Set("Content-Type", "application/json; charset=utf-8")
	ctx.ResponseWriter().Header().Set("Content-Length", strconv.Itoa(len(output)))
	ctx.ResponseWriter().WriteHeader(http.StatusOK)
	ctx.ResponseWriter().Write(output)
	return http.StatusOK, err
}

type RedirectRenderer struct{}

func (w *RedirectRenderer) Type() string {
	return "REDIRECT"
}

func (w *RedirectRenderer) Write(ctx *Context, page *MirrorlistPage) (statusCode int, err error) {
	ctx.ResponseWriter().Header().Set("Content-Type", "text/html; charset=utf-8")
	if len(page.MirrorList) > 0 {
		for i, m := range page.MirrorList[1:] {
			ctx.ResponseWriter().Header().Add("Link", fmt.Sprintf("<%s>; rel=duplicate; pri=%d; geo=%s", m.HttpURL+page.FileInfo.Path[1:], i+1, strings.ToLower(m.CountryFields[0])))
		}
		http.Redirect(ctx.ResponseWriter(), ctx.Request(), page.MirrorList[0].HttpURL+page.FileInfo.Path[1:], http.StatusFound)
		return http.StatusFound, nil
	}
	http.NotFound(ctx.ResponseWriter(), ctx.Request())
	return http.StatusNotFound, nil
}

type MirrorListRenderer struct{}

func (w *MirrorListRenderer) Type() string {
	return "MIRRORLIST"
}

func (w *MirrorListRenderer) Write(ctx *Context, page *MirrorlistPage) (statusCode int, err error) {
	if ctx.Templates() == nil {
		return 0, TemplatesNotFound
	}
	sort.Sort(ByExcludeReason{page.ExcludedList})
	page.MapURL = getMirrorMapUrl(page.MirrorList, page.ClientInfo)
	ctx.ResponseWriter().Header().Set("Content-Type", "text/html; charset=utf-8")
	err = ctx.Templates().ExecuteTemplate(ctx.ResponseWriter(), "mirrorlist", page)
	if err != nil {
		return http.StatusInternalServerError, err
	}
	return http.StatusOK, err
}
