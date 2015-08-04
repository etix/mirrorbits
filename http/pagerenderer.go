// Copyright (c) 2014-2015 Ludovic Fauvet
// Licensed under the MIT license

package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/etix/mirrorbits/mirrors"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

var (
	TemplatesNotFound = errors.New("Please set a valid path to the templates directory.")
)

type ResultsRenderer interface {
	Write(ctx *Context, results *mirrors.Results) (int, error)
	Type() string
}

// JsonRenderer is used to render JSON formatted details about the current request
type JsonRenderer struct{}

func (w *JsonRenderer) Type() string {
	return "JSON"
}

func (w *JsonRenderer) Write(ctx *Context, results *mirrors.Results) (statusCode int, err error) {
	var output []byte

	if ctx.IsPretty() {
		output, err = json.MarshalIndent(results, "", "    ")
	} else {
		output, err = json.Marshal(results)
	}

	if err != nil {
		return http.StatusInternalServerError, err
	}

	ctx.ResponseWriter().Header().Set("Content-Type", "application/json; charset=utf-8")
	ctx.ResponseWriter().Header().Set("Content-Length", strconv.Itoa(len(output)))
	ctx.ResponseWriter().Write(output)
	return http.StatusOK, nil
}

// RedirectRenderer is a basic renderer that redirects the user to the first mirror in the list
type RedirectRenderer struct{}

func (w *RedirectRenderer) Type() string {
	return "REDIRECT"
}

func (w *RedirectRenderer) Write(ctx *Context, results *mirrors.Results) (statusCode int, err error) {
	if len(results.MirrorList) > 0 {
		ctx.ResponseWriter().Header().Set("Content-Type", "text/html; charset=utf-8")

		path := strings.TrimPrefix(results.FileInfo.Path, "/")

		// Generate the header alternative links
		for i, m := range results.MirrorList[1:] {
			var countryCode string
			if len(m.CountryFields) > 0 {
				countryCode = strings.ToLower(m.CountryFields[0])
			}
			ctx.ResponseWriter().Header().Add("Link", fmt.Sprintf("<%s>; rel=duplicate; pri=%d; geo=%s", m.HttpURL+path, i+1, countryCode))
		}

		// Finally issue the redirect
		http.Redirect(ctx.ResponseWriter(), ctx.Request(), results.MirrorList[0].HttpURL+path, http.StatusFound)
		return http.StatusFound, nil
	}
	// No mirror returned for this request
	http.NotFound(ctx.ResponseWriter(), ctx.Request())
	return http.StatusNotFound, nil
}

// MirrorListRenderer is used to render the mirrorlist page using the HTML templates
type MirrorListRenderer struct{}

func (w *MirrorListRenderer) Type() string {
	return "MIRRORLIST"
}

func (w *MirrorListRenderer) Write(ctx *Context, results *mirrors.Results) (statusCode int, err error) {
	if ctx.Templates().mirrorlist == nil {
		// No templates found for the mirrorlist
		return http.StatusInternalServerError, TemplatesNotFound
	}
	// Sort the exclude reasons by message so they appear grouped
	sort.Sort(mirrors.ByExcludeReason{results.ExcludedList})

	// Create a temporary output buffer to render the page
	var buf bytes.Buffer

	// Generate the URL to the map
	results.MapURL = mirrors.GetMirrorMapUrl(results.MirrorList, results.ClientInfo)
	ctx.ResponseWriter().Header().Set("Content-Type", "text/html; charset=utf-8")

	// Render the page into the buffer
	err = ctx.Templates().mirrorlist.ExecuteTemplate(&buf, "base", results)
	if err != nil {
		// Something went wrong, discard the buffer
		return http.StatusInternalServerError, err
	}

	// Write the buffer to the socket
	buf.WriteTo(ctx.ResponseWriter())
	return http.StatusOK, nil
}
