// Copyright (c) 2014-2015 Ludovic Fauvet
// Licensed under the MIT license

package logs

import (
	"bytes"
	"errors"
	"github.com/etix/mirrorbits/filesystem"
	"github.com/etix/mirrorbits/mirrors"
	"github.com/etix/mirrorbits/network"
	"io/ioutil"
	"os"
	"strings"
	"testing"
)

type CloseTester struct {
	closed bool
}

func (c *CloseTester) Write(p []byte) (n int, err error) {
	return 0, err
}

func (c *CloseTester) Close() error {
	c.closed = true
	return nil
}

func TestDownloadsLogger_Close(t *testing.T) {
	f := &CloseTester{}

	dlogger.f = f

	if f.closed == true {
		t.Fatalf("Precondition failed")
	}

	dlogger.Close()

	if f.closed == false {
		t.Fatalf("Should be closed")
	}

	if dlogger.l != nil || dlogger.f != nil {
		t.Fatalf("Should be nil")
	}
}

func TestOpenLogFile(t *testing.T) {
	path, err := ioutil.TempDir("", "mirrorbits-tests")
	if err != nil {
		t.Errorf("Unable to create temporary directory: %s", err.Error())
		return
	}
	defer os.RemoveAll(path)

	f, newfile, err := openLogFile(path + "/test1.log")
	if err != nil {
		t.Fatalf("Unexpected error: %s", err.Error())
	}

	if newfile == false {
		t.Fatalf("Expected new file")
	}

	content := []byte("It works!")
	n, err := f.Write(content)
	if err != nil {
		t.Fatalf("Unexpected write error: %s", err.Error())
	}
	if n != len(content) {
		t.Fatalf("Invalid number of bytes written")
	}
	f.Close()

	/* Reopen file to check newfile */
	f, newfile, err = openLogFile(path + "/test1.log")
	if err != nil {
		t.Fatalf("Unexpected error: %s", err.Error())
	}

	if newfile == true {
		t.Fatalf("Expected newfile to be false")
	}
	f.Close()

	/* Open invalid file */
	f, newfile, err = openLogFile("")
	if err == nil {
		t.Fatalf("Error expected while opening invalid file")
	}
	f.Close()
}

func TestSetDownloadLogWriter(t *testing.T) {
	if dlogger.l != nil || dlogger.f != nil {
		t.Fatalf("Precondition failed")
	}

	var buf bytes.Buffer

	setDownloadLogWriter(&buf, true)

	if dlogger.l == nil {
		t.Fatalf("Logger not created")
	}

	if buf.Len() == 0 {
		t.Fatalf("Buffer empty, expected header")
	}

	if !strings.HasPrefix(buf.String(), "#") {
		t.Fatalf("Header doesn't starts with '#'")
	}

	buf.Reset()

	/* */

	setDownloadLogWriter(&buf, false)

	if buf.Len() != 0 {
		t.Fatalf("Expected no content")
	}
}

func TestReloadDownloadLogs(t *testing.T) {
	// Not implemented because of GetConfig()
	// TODO need abstraction for GetConfig()
}

//type xResults struct {
//	FileInfo     filesystem.FileInfo
//	MapURL       string `json:"-"`
//	IP           string
//	ClientInfo   network.GeoIPRecord
//	MirrorList   Mirrors
//	ExcludedList Mirrors `json:",omitempty"`
//	Fallback     bool    `json:",omitempty"`
//}

func TestLogDownload(t *testing.T) {
	var buf bytes.Buffer

	dlogger.Close()

	// The next line isn't supposed to crash.
	LogDownload("", 500, nil, nil)

	setDownloadLogWriter(&buf, true)

	buf.Reset()

	// The next few lines arent't supposed to crash.
	LogDownload("", 200, nil, nil)
	LogDownload("", 302, nil, nil)
	LogDownload("", 404, nil, nil)
	LogDownload("", 500, nil, nil)
	LogDownload("", 501, nil, nil)

	if c := strings.Count(buf.String(), "\n"); c != 5 {
		t.Fatalf("Invalid number of lines, got %d, expected 5", c)
	}

	buf.Reset()

	/* */
	p := &mirrors.Results{
		FileInfo: filesystem.FileInfo{
			Path: "/test/file.tgz",
		},
		MirrorList: mirrors.Mirrors{
			mirrors.Mirror{
				ID:            "m1",
				Asnum:         444,
				Distance:      99,
				CountryFields: []string{"FR", "UK", "DE"},
			},
			mirrors.Mirror{
				ID: "m2",
			},
		},
		IP: "192.168.0.1",
		ClientInfo: network.GeoIPRecord{
			ASNum: 444,
		},
		Fallback: true,
	}

	LogDownload("JSON", 200, p, nil)

	expected := "JSON 200 \"/test/file.tgz\" ip:192.168.0.1 mirror:m1 fallback:true sameasn:444 distance:99.00km countries:FR,UK,DE\n"
	if !strings.HasSuffix(buf.String(), expected) {
		t.Fatalf("Invalid log line:\nGot:\n%#vs\nExpected:\n%#v", buf.String(), expected)
	}

	buf.Reset()

	/* */
	p = &mirrors.Results{
		FileInfo: filesystem.FileInfo{
			Path: "/test/file.tgz",
		},
		IP: "192.168.0.1",
	}

	LogDownload("JSON", 404, p, nil)

	expected = "JSON 404 \"/test/file.tgz\" ip:192.168.0.1\n"
	if !strings.HasSuffix(buf.String(), expected) {
		t.Fatalf("Invalid log line:\nGot:\n%#vs\nExpected:\n%#v", buf.String(), expected)
	}

	buf.Reset()

	/* */
	p = &mirrors.Results{
		MirrorList: mirrors.Mirrors{
			mirrors.Mirror{
				ID: "m1",
			},
			mirrors.Mirror{
				ID: "m2",
			},
		},
	}

	LogDownload("JSON", 500, p, errors.New("test error"))

	expected = "JSON 500 \"\" ip: mirror:m1 error:test error\n"
	if !strings.HasSuffix(buf.String(), expected) {
		t.Fatalf("Invalid log line:\nGot:\n%#vs\nExpected:\n%#v", buf.String(), expected)
	}

	buf.Reset()

	/* */
	p = &mirrors.Results{
		FileInfo: filesystem.FileInfo{
			Path: "/test/file.tgz",
		},
		IP: "192.168.0.1",
	}

	LogDownload("JSON", 501, p, errors.New("test error"))

	expected = "JSON 501 \"/test/file.tgz\" ip:192.168.0.1 error:test error\n"
	if !strings.HasSuffix(buf.String(), expected) {
		t.Fatalf("Invalid log line:\nGot:\n%#vs\nExpected:\n%#v", buf.String(), expected)
	}

	buf.Reset()
}
