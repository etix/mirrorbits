// Copyright (c) 2025 Arnaud Rebillout
// Licensed under the MIT license

package http

import (
	"errors"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"os"
	"path"
	"reflect"
	"strings"
	"syscall"
	"testing"

	. "github.com/etix/mirrorbits/config"
	"github.com/etix/mirrorbits/core"
	"github.com/etix/mirrorbits/mirrors"
	. "github.com/etix/mirrorbits/testing"
	"github.com/rafaeljusto/redigomock"
)

var (
	fallbackURL = "http://fallback.mirror/"
	mirrorURL = "http://example.mirror/"
	testFile = "/testy.tgz"
	testFileSize = "48"
	testFileModTime = "2025-06-01 06:00:00.123456789 +0000 UTC"
	testFileSha256 = "1235a5b376903794b373d84ed615bb36013e70ed6aebf30b2f4823321d5182ec"
	testFileLastModified = "Sun, 01 Jun 2025 06:00:00 GMT"
)

// Join URL and a path
func urlJoinPath(url, filepath string) string {
	return url + strings.TrimLeft(filepath, "/")
}

// Create an empty file within a directory, fail if it already exists
func makeEmptyFile(dir, filename string) error {
	filePath := path.Join(dir, filename)
	fileFlags := os.O_CREATE|os.O_EXCL|os.O_WRONLY
	f, err := os.OpenFile(filePath, fileFlags, 0644)
	if err != nil {
		return err
	}
	return f.Close()
}

// Make a request
func makeRequest(method, url string, headers map[string]string) *http.Request {
	req := httptest.NewRequest(method, url, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

// Make a response, as returned by mirrorbits
func makeResponse(code int, headers map[string]string) *http.Response {
	var resp http.Response

	switch code {
	case 302:
		resp = http.Response{
			Status:	    "302 Found",
			StatusCode: 302,
			Proto:      "HTTP/1.1",
			ProtoMajor: 1,
			ProtoMinor: 1,
			Header: http.Header{
				"Cache-Control": {"private, no-cache"},
				"Content-Type": {"text/html; charset=utf-8"},
				"Server": {"Mirrorbits/"+core.VERSION},
			},
			ContentLength: -1,
		}
	case 304:
		resp = http.Response{
			Status:	    "304 Not Modified",
			StatusCode: 304,
			Proto:      "HTTP/1.1",
			ProtoMajor: 1,
			ProtoMinor: 1,
			Header: http.Header{
				"Server": {"Mirrorbits/"+core.VERSION},
			},
			ContentLength: -1,
		}
	case 403:
		resp = http.Response{
			Status:	    "403 Forbidden",
			StatusCode: 403,
			Proto:      "HTTP/1.1",
			ProtoMajor: 1,
			ProtoMinor: 1,
			Header: http.Header{
				"Content-Type": {"text/plain; charset=utf-8"},
				"Server": {"Mirrorbits/"+core.VERSION},
				"X-Content-Type-Options": {"nosniff"},
			},
			ContentLength: -1,
		}
	case 404:
		resp = http.Response{
			Status:	    "404 Not Found",
			StatusCode: 404,
			Proto:      "HTTP/1.1",
			ProtoMajor: 1,
			ProtoMinor: 1,
			Header: http.Header{
				"Content-Type": {"text/plain; charset=utf-8"},
				"Server": {"Mirrorbits/"+core.VERSION},
				"X-Content-Type-Options": {"nosniff"},
			},
			ContentLength: -1,
		}
	default:
		resp = http.Response{}
	}

	for k, v := range headers {
		resp.Header.Set(k, v)
	}
		
	return &resp
}

// Do a request and return the response
func doRequest(h *HTTP, method string, url string, headers map[string]string) (*http.Response) {
	req := makeRequest(method, url, headers)
	recorder := httptest.NewRecorder()
	// Note: requestDispatcher calls mirrorHandler
	h.requestDispatcher(recorder, req)
	return recorder.Result()
}

// Check if two http.Response are equal, excluding the body
func respEqual(r1 *http.Response, r2 *http.Response) bool {
	r1Body, r2Body := r1.Body, r2.Body
	r1.Body, r2.Body = nil, nil
	res := reflect.DeepEqual(r1, r2)
	r1.Body, r2.Body = r1Body, r2Body
	return res
}

// Dump a response, excluding the body, no error checking
func dump(resp *http.Response) string {
	dump, _ := httputil.DumpResponse(resp, false)
	return string(dump)
}

// Return the following error:
// dial tcp 127.0.0.1:6379: connect: connection refused
func connectionRefusedError() error {
	ip := net.ParseIP("127.0.0.1")
	tcpAddr := &net.TCPAddr{IP: ip, Port: 6379}
	var addr net.Addr = tcpAddr
	syscallErr := os.NewSyscallError("connect", syscall.ECONNREFUSED)
	return &net.OpError{Op: "dial", Net: "tcp", Addr: addr, Err: syscallErr}
}

// Return the following error:
// LOADING Redis is loading the dataset in memory
func redisIsLoadingError() error {
	return errors.New("LOADING Redis is loading the dataset in memory")
}

// A pair consisting of a redis command, and its expected result
type mockedCmd struct {
	Cmd []string
	Res interface{}
}

// Register a list of mocked redis commands
func mockCommands(mock *redigomock.Conn, commands []mockedCmd) {
	for _, item := range commands {
		// Craft arguments for mock.Command, then mock
		args := []interface{}{}
		for _, arg := range item.Cmd[1:] {
			args = append(args, arg)
		}
		cmd := mock.Command(item.Cmd[0], args...)

		// Add an expectation
		switch item.Res.(type) {
			case error:
				cmd.ExpectError(item.Res.(error))
			case []string:
				cmd.ExpectStringSlice(item.Res.([]string)...)
			case map[string]string:
				cmd.ExpectMap(item.Res.(map[string]string))
			default:
				// unknown type? that's a programming error

		}
	}
}

// Wrapper around redigomock.ExpectationsWereMet() to return a slice of errors
func getMockErrors(mock *redigomock.Conn) (result []error) {
	err := mock.ExpectationsWereMet()
	if err != nil {
		lines := strings.Split(err.Error(), "\n")
		for _, line := range lines {
			line := strings.TrimSpace(line)
			if line == "" {
				continue
			}
			// A PING command might or might not have been sent during
			// the tests (due to `ConnectPubsub()` I believe). Since we
			// don't mock it, we must filter it out from the errors.
			if strings.HasPrefix(line, "command PING ") &&
				strings.HasSuffix(line, " not registered in redigomock library") {
				continue
			}
			result = append(result, errors.New(line))
		}
	}
	return
}

// Context for a test
type testContext struct {
	TestDir     string
	RepoDir     string
	MockedConn  *redigomock.Conn
	MirrorCache *mirrors.Cache
	Server      *HTTP
}

// Prepare a test, return the context
func prepareTest(filenames []string) (testContext, error) {
	// Create a temporary directory for test data
	testDir, err := ioutil.TempDir("", "mirrorbits-tests")
	if err != nil {
		return testContext{}, err
	}

	defer func() {
		if err != nil {
			os.RemoveAll(testDir)
		}
	}()

	// Create the repo directory, along with dummy files
	repoDir := testDir + "/repo"
	err = os.Mkdir(repoDir, 0755)
	if err != nil {
		return testContext{}, err
	}

	for _, f := range filenames {
		err = makeEmptyFile(repoDir, f)
		if err != nil {
			return testContext{}, err
		}
	}

	// Create the templates directory, along with dummy templates
	templatesDir := testDir + "/templates"
	err = os.Mkdir(templatesDir, 0755)
	if err != nil {
		return testContext{}, err
	}

	templates := []string{"base.html", "mirrorlist.html", "mirrorstats.html"}
	for _, f := range templates {
		err = makeEmptyFile(templatesDir, f)
		if err != nil {
			return testContext{}, err
		}
	}

	// Set mirrorbits configuration
	SetConfiguration(&Configuration{
		Repository: repoDir,
		Templates: templatesDir,
		OutputMode: "redirect",
		MaxLinkHeaders: 5,
		Fallbacks: []Fallback{
			{URL: fallbackURL},
		},
	})

	// Reset the default server before each test. Must be done before
	// creating a HTTPServer instance, otherwise we run into:
	//
	//   panic: http: multiple registrations for /
	//
	// Cf. https://stackoverflow.com/a/40790728/
	http.DefaultServeMux = new(http.ServeMux)

	// Setup HTTP server
	mock, conn := PrepareRedisTest()
	conn.ConnectPubsub()
	cache := mirrors.NewCache(conn)
	h := HTTPServer(conn, cache)

	// Ready for testing!
	return testContext {
		TestDir: testDir,
		RepoDir: repoDir,
		MockedConn: mock,
		MirrorCache: cache,
		Server: h,
	}, nil
}

// Cleanup after a test is done
func cleanupTest(ctx testContext) {
	if ctx.TestDir != "" {
		os.RemoveAll(ctx.TestDir)
	}
}

// Test 4xx return codes from MirrorHandler.
//
// Those HTTP codes are triggered when the file requested doesn't even exist in
// the local repo. Mirrorbits doesn't query the database in those cases, so
// there's no need to mock redis commands.
func TestMirrorHandler4xx(t *testing.T) {
	// Prepare
	ctx, err := prepareTest([]string{})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupTest(ctx)

	noHeader := map[string]string{}

	// Request a file that doesn't exist on the local repo
	// -> return 404 "Not Found"
	resp := doRequest(ctx.Server, "GET", "/foobar", noHeader)
	want := makeResponse(404, noHeader)
	if !respEqual(want, resp) {
		t.Fatalf("Expected: %v, got: %v", want, resp)
	}

	// Request a file outside of the local repo
	// -> return 403 "Forbidden"
	resp = doRequest(ctx.Server, "GET", "/../foobar", noHeader)
	want = makeResponse(403, noHeader)
	if !respEqual(want, resp) {
		t.Fatalf("Expected: %v, got: %v", want, resp)
	}

	// Request a file while the repo directory doesn't even exist
	// -> return 404 "Not Found"
	if err = os.Remove(ctx.RepoDir); err != nil {
		t.Fatal(err)
	}
	resp = doRequest(ctx.Server, "GET", "/foobar", noHeader)
	want = makeResponse(404, noHeader)
	if !respEqual(want, resp) {
		t.Fatalf("Expected: %v, got: %v", want, resp)
	}
}

var mockedCmds302Fallback = [][]mockedCmd{
	// Database is unreachable (redis error "connection refused")
	{
		{
			Cmd: []string{"HMGET", "FILE_"+testFile, "size", "modTime", "sha1", "sha256", "md5"},
			Res: connectionRefusedError(),
		},
	},
	// Database is loading
	{
		{
			Cmd: []string{"HMGET", "FILE_"+testFile, "size", "modTime", "sha1", "sha256", "md5"},
			Res: redisIsLoadingError(),
		},
	},
	// Database is reachable. File exists in the local repo, but is not
	// found in the database (in real-life, it means that the local repo
	// was updated with new files, but mirrorbits didn't rescan it yet)
	{
		{
			Cmd: []string{"HMGET", "FILE_"+testFile, "size", "modTime", "sha1", "sha256", "md5"},
			Res: []string{"", "", "", "", "", ""},
		},
	},
	// Database is reachable, file exists in the local repo, and is also
	// present in the database, however no mirror have this file yet
	{
		{
			Cmd: []string{"HMGET", "FILE_"+testFile, "size", "modTime", "sha1", "sha256", "md5"},
			Res: []string{testFileSize, testFileModTime, "", testFileSha256, ""},
		},
		{
			Cmd: []string{"SMEMBERS", "FILEMIRRORS_"+testFile},
			Res: []string{},
		},
	},
}

var mockedCmds302Mirror = [][]mockedCmd{
	// Database is reachable, file exists in the local repo, is also
	// present in the database, and is found on a mirror.
	//
	// Note: At startup, mirrorbits says "Can't load the GeoIP databases,
	// all requests will be served by the fallback mirrors". Well it
	// doesn't seem to be true, as this test case shows.
	{
		{
			Cmd: []string{"HMGET", "FILE_"+testFile, "size", "modTime", "sha1", "sha256", "md5"},
			Res: []string{testFileSize, testFileModTime, "", testFileSha256, ""},
		},
		{
			Cmd: []string{"SMEMBERS", "FILEMIRRORS_"+testFile},
			Res: []string{"42"},
		},
		{
			Cmd: []string{"HGETALL", "MIRROR_42"},
			Res: map[string]string{
				"ID":      "42",
				"http":    mirrorURL,
				"enabled": "true",
				"httpUp":  "true",
			},
		},
		{
			Cmd: []string{"HMGET", "FILEINFO_42_"+testFile, "size", "modTime", "sha1", "sha256", "md5"},
			Res: []string{testFileSize, testFileModTime, "", "", ""},
		},
	},
}

var mockedCmds304 = [][]mockedCmd{
	// File exists in the database, and is older than the If-Modified-Since
	// request header, so mirrorbits returns early and doesn't even check
	// if mirrors have the file.
	{
		{
			Cmd: []string{"HMGET", "FILE_"+testFile, "size", "modTime", "sha1", "sha256", "md5"},
			Res: []string{testFileSize, testFileModTime, "", testFileSha256, ""},
		},
	},
}

// Test 3xx status codes.
//
// Mocking redis can be tricky. If we forget to mock a command, we'll get an
// error of the type:
//
//   command [...] not registered in redigomock library
//
// However a redis error makes mirrorbits bail out early from mirror selection,
// and in turns it triggers a fallback redirection. So from the outside, all we
// know is that yes, mirrorbits returned a fallback redirect, and maybe that's
// what we expect, so the test pass, but in fact it passed _because_ we forgot
// to mock a redis command!
//
// That's why it's not enough to just check if mocked commands were called, we
// also need to make sure that redigomock didn't return any error that were
// unexpected.
func TestMirrorHandler3xx(t *testing.T) {
	// Prepare
	ctx, err := prepareTest([]string{testFile})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupTest(ctx)

	// Define tests
	tests := map[string]struct {
		MockedCommands [][]mockedCmd
		RequestHeaders map[string]string
		Response       *http.Response
	} {
		// Test various scenarios that lead to a fallback redirection
		"fallback_redirect": {
			MockedCommands: mockedCmds302Fallback,
			Response: makeResponse(302, map[string]string{
				"Location": urlJoinPath(fallbackURL, testFile),
			}),
		},
		// Same as above, but this time passing a If-Modified-Since
		// header, set to an old date, so no consequence on the result
		"fallback_redirect_old_if_modified_since": {
			MockedCommands: mockedCmds302Fallback,
			RequestHeaders: map[string]string{
				"If-Modified-Since": "Tue, 01 Jun 1999 00:00:00 GMT",
			},
			Response: makeResponse(302, map[string]string{
				"Location": urlJoinPath(fallbackURL, testFile),
			}),
		},
		// Test mirror redirection
		"mirror_redirect": {
			MockedCommands: mockedCmds302Mirror,
			Response: makeResponse(302, map[string]string{
				"Location": urlJoinPath(mirrorURL, testFile),
			}),
		},
		// Same as above, but with a old If-Modified-Since
		"mirror_redirect_old_if_modified_since": {
			MockedCommands: mockedCmds302Mirror,
			RequestHeaders: map[string]string{
				"If-Modified-Since": "Tue, 01 Jun 1999 00:00:00 GMT",
			},
			Response: makeResponse(302, map[string]string{
				"Location": urlJoinPath(mirrorURL, testFile),
			}),
		},
		// Test "304 Not Modified" by setting a If-Modified-Since header
		// that is newer that the test file modification time
		"not_modified": {
			MockedCommands: mockedCmds304,
			RequestHeaders: map[string]string{
				"If-Modified-Since": "Wed, 04 Jun 2025 02:12:35 GMT",
			},
			Response:  makeResponse(304, map[string]string{
				"Last-Modified": testFileLastModified,
			}),
		},
	}
	
	// Run tests
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			for i, commands := range tt.MockedCommands {
				// Register mocked commands
				mockCommands(ctx.MockedConn, commands)

				// Request the file
				resp := doRequest(ctx.Server, "GET", testFile, tt.RequestHeaders)

				// Check that mocking went fine
				for _, err := range getMockErrors(ctx.MockedConn) {
					t.Errorf("#%d: %s", i, err)
				}

				// Check that response is as expected
				if !respEqual(tt.Response, resp) {
					//t.Errorf("#%d: Expected: %v, got: %v", i, tt.Response, resp)
					t.Errorf("#%d: Expected:\n%sGot:\n%s", i, dump(tt.Response), dump(resp))
				}

				// Cleanup
				ctx.MockedConn.Clear()
				ctx.MirrorCache.Clear()
			}
		})
	}
}
