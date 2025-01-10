// Copyright (c) 2014-2019 Ludovic Fauvet
// Licensed under the MIT license

package mirrors

import (
	"fmt"
	"reflect"
	"strconv"
	"testing"
	"time"
	"unsafe"

	"github.com/etix/mirrorbits/filesystem"
	"github.com/etix/mirrorbits/network"
	. "github.com/etix/mirrorbits/testing"
	_ "github.com/rafaeljusto/redigomock"
)

func TestNewCache(t *testing.T) {
	_, conn := PrepareRedisTest()
	conn.ConnectPubsub()

	c := NewCache(nil)
	if c != nil {
		t.Fatalf("Expected invalid instance")
	}

	c = NewCache(conn)
	if c == nil {
		t.Fatalf("No valid instance returned")
	}
}

type TestValue struct {
	value string
}

func (f *TestValue) Size() int {
	return int(unsafe.Sizeof(f.value))
}

func TestCache_Clear(t *testing.T) {
	_, conn := PrepareRedisTest()
	conn.ConnectPubsub()

	c := NewCache(conn)

	c.fiCache.Set("test", &TestValue{"42"})
	c.fmCache.Set("test", &TestValue{"42"})
	c.mCache.Set("test", &TestValue{"42"})
	c.fimCache.Set("test", &TestValue{"42"})

	c.Clear()

	if _, ok := c.fiCache.Get("test"); ok {
		t.Fatalf("Value shouldn't be present")
	}
	if _, ok := c.fmCache.Get("test"); ok {
		t.Fatalf("Value shouldn't be present")
	}
	if _, ok := c.mCache.Get("test"); ok {
		t.Fatalf("Value shouldn't be present")
	}
	if _, ok := c.fimCache.Get("test"); ok {
		t.Fatalf("Value shouldn't be present")
	}
}

func assertFileInfoEqual(t *testing.T, actual *filesystem.FileInfo, expected *filesystem.FileInfo) {
	t.Helper()
	if actual.Path != expected.Path {
		t.Fatalf("Path doesn't match, expected %#v got %#v", expected.Path, actual.Path)
	}
	if actual.Size != expected.Size {
		t.Fatalf("Size doesn't match, expected %#v got %#v", expected.Size, actual.Size)
	}
	if !actual.ModTime.Equal(expected.ModTime) {
		t.Fatalf("ModTime doesn't match, expected %s got %s", expected.ModTime.String(), actual.ModTime.String())
	}
	if actual.Sha1 != expected.Sha1 {
		t.Fatalf("Sha1 doesn't match, expected %#v got %#v", expected.Sha1, actual.Sha1)
	}
	if actual.Sha256 != expected.Sha256 {
		t.Fatalf("Sha256 doesn't match, expected %#v got %#v", expected.Sha256, actual.Sha256)
	}
	if actual.Md5 != expected.Md5 {
		t.Fatalf("Md5 doesn't match, expected %#v got %#v", expected.Md5, actual.Md5)
	}
}

func TestCache_fetchFileInfo(t *testing.T) {
	mock, conn := PrepareRedisTest()
	conn.ConnectPubsub()

	c := NewCache(conn)

	testfile := filesystem.FileInfo{
		Path:    "/test/file.tgz",
		Size:    43000,
		ModTime: time.Now(),
		Sha1:    "3ce963aea2d6f23fe915063f8bba21888db0ddfa",
		Sha256:  "1c8e38c7e03e4d117eba4f82afaf6631a9b79f4c1e9dec144d4faf1d109aacda",
		Md5:     "2c98ec39f49da6ddd9cfa7b1d7342afe",
	}

	f, err := c.fetchFileInfo(testfile.Path)
	if err == nil {
		t.Fatalf("Error expected, mock command not yet registered")
	}

	cmdGetFileinfo := mock.Command("HMGET", "FILE_"+testfile.Path, "size", "modTime", "sha1", "sha256", "md5").Expect([]interface{}{
		[]byte(strconv.FormatInt(testfile.Size, 10)),
		[]byte(testfile.ModTime.Format("2006-01-02 15:04:05.999999999 -0700 MST")),
		[]byte(testfile.Sha1),
		[]byte(testfile.Sha256),
		[]byte(testfile.Md5),
	})

	f, err = c.fetchFileInfo(testfile.Path)
	if err != nil {
		t.Fatalf("Unexpected error: %s", err.Error())
	}

	if mock.Stats(cmdGetFileinfo) < 1 {
		t.Fatalf("HMGET not executed")
	}

	assertFileInfoEqual(t, &f, &testfile)

	_, ok := c.fiCache.Get(testfile.Path)
	if !ok {
		t.Fatalf("Not stored in cache")
	}
}

func TestCache_fetchFileInfo_non_existing(t *testing.T) {
	mock, conn := PrepareRedisTest()
	conn.ConnectPubsub()

	c := NewCache(conn)

	testfile := filesystem.FileInfo{
		Path:    "/test/file.tgz",
		Size:    0,
		ModTime: time.Time{},
		Sha1:    "",
		Sha256:  "",
		Md5:     "",
	}

	f, err := c.fetchFileInfo(testfile.Path)
	if err == nil {
		t.Fatalf("Error expected, mock command not yet registered")
	}

	cmdGetFileinfo := mock.Command("HMGET", "FILE_"+testfile.Path, "size", "modTime", "sha1", "sha256", "md5").Expect([]interface{}{
		[]byte(""),
		[]byte(""),
		[]byte(""),
		[]byte(""),
		[]byte(""),
	})

	f, err = c.fetchFileInfo(testfile.Path)
	// fetchFileInfo on a non-existing file doesn't yield Redis.ErrNil
	if err != nil {
		t.Fatalf("Unexpected error: %s", err.Error())
	}

	if mock.Stats(cmdGetFileinfo) < 1 {
		t.Fatalf("HMGET not executed")
	}

	assertFileInfoEqual(t, &f, &testfile)

	// Non-existing file are also stored in cache
	_, ok := c.fiCache.Get(testfile.Path)
	if !ok {
		t.Fatalf("Not stored in cache")
	}
}

func TestCache_GetFileInfo(t *testing.T) {
	mock, conn := PrepareRedisTest()
	conn.ConnectPubsub()

	c := NewCache(conn)

	testfile := filesystem.FileInfo{
		Path:    "/test/file.tgz",
		Size:    43000,
		ModTime: time.Now(),
		Sha1:    "3ce963aea2d6f23fe915063f8bba21888db0ddfa",
		Sha256:  "1c8e38c7e03e4d117eba4f82afaf6631a9b79f4c1e9dec144d4faf1d109aacda",
		Md5:     "2c98ec39f49da6ddd9cfa7b1d7342afe",
	}

	_, err := c.GetFileInfo(testfile.Path)
	if err == nil {
		t.Fatalf("Error expected, mock command not yet registered")
	}

	cmdGetFileinfo := mock.Command("HMGET", "FILE_"+testfile.Path, "size", "modTime", "sha1", "sha256", "md5").Expect([]interface{}{
		[]byte(strconv.FormatInt(testfile.Size, 10)),
		[]byte(testfile.ModTime.Format("2006-01-02 15:04:05.999999999 -0700 MST")),
		[]byte(testfile.Sha1),
		[]byte(testfile.Sha256),
		[]byte(testfile.Md5),
	})

	f, err := c.GetFileInfo(testfile.Path)
	if err != nil {
		t.Fatalf("Unexpected error: %s", err.Error())
	}

	if mock.Stats(cmdGetFileinfo) < 1 {
		t.Fatalf("HMGET not executed")
	}

	assertFileInfoEqual(t, &f, &testfile)

	f, err = c.GetFileInfo(testfile.Path)
	if err != nil {
		t.Fatalf("Unexpected error: %s", err.Error())
	}
	if mock.Stats(cmdGetFileinfo) > 1 {
		t.Fatalf("Cache not used, request expected to be done once")
	}

	assertFileInfoEqual(t, &f, &testfile)
}

func TestCache_GetFileInfo_non_existing(t *testing.T) {
	mock, conn := PrepareRedisTest()
	conn.ConnectPubsub()

	c := NewCache(conn)

	testfile := filesystem.FileInfo{
		Path:    "/test/file.tgz",
		Size:    0,
		ModTime: time.Time{},
		Sha1:    "",
		Sha256:  "",
		Md5:     "",
	}

	_, err := c.GetFileInfo(testfile.Path)
	if err == nil {
		t.Fatalf("Error expected, mock command not yet registered")
	}

	cmdGetFileinfo := mock.Command("HMGET", "FILE_"+testfile.Path, "size", "modTime", "sha1", "sha256", "md5").Expect([]interface{}{
		[]byte(""),
		[]byte(""),
		[]byte(""),
		[]byte(""),
		[]byte(""),
	})

	f, err := c.GetFileInfo(testfile.Path)
	// GetFileInfo on a non-existing file doesn't yield Redis.ErrNil
	if err != nil {
		t.Fatalf("Unexpected error: %s", err.Error())
	}

	if mock.Stats(cmdGetFileinfo) < 1 {
		t.Fatalf("HMGET not executed")
	}

	assertFileInfoEqual(t, &f, &testfile)

	f, err = c.GetFileInfo(testfile.Path)
	if err != nil {
		t.Fatalf("Unexpected error: %s", err.Error())
	}
	// Non-existing file are also stored in cache
	if mock.Stats(cmdGetFileinfo) > 1 {
		t.Fatalf("Cache not used, request expected to be done once")
	}

	assertFileInfoEqual(t, &f, &testfile)
}

func TestCache_fetchFileMirrors(t *testing.T) {
	mock, conn := PrepareRedisTest()
	conn.ConnectPubsub()

	c := NewCache(conn)
	filename := "/test/file.tgz"

	_, err := c.fetchFileMirrors(filename)
	if err == nil {
		t.Fatalf("Error expected, mock command not yet registered")
	}

	cmdGetFilemirrors := mock.Command("SMEMBERS", "FILEMIRRORS_"+filename).Expect([]interface{}{
		[]byte("9"),
		[]byte("2"),
		[]byte("5"),
	})

	ids, err := c.fetchFileMirrors(filename)
	if err != nil {
		t.Fatalf("Unexpected error: %s", err.Error())
	}

	if mock.Stats(cmdGetFilemirrors) < 1 {
		t.Fatalf("SMEMBERS not executed")
	}

	if len(ids) != 3 {
		t.Fatalf("Invalid number of items returned")
	}

	_, ok := c.fmCache.Get(filename)
	if !ok {
		t.Fatalf("Not stored in cache")
	}
}

func TestCache_fetchMirror(t *testing.T) {
	mock, conn := PrepareRedisTest()
	conn.ConnectPubsub()

	c := NewCache(conn)

	testmirror := Mirror{
		ID:             1,
		Name:           "m1",
		HttpURL:        "http://m1.mirror",
		RsyncURL:       "rsync://m1.mirror",
		FtpURL:         "ftp://m1.mirror",
		SponsorName:    "m1sponsor",
		SponsorURL:     "m1sponsorurl",
		SponsorLogoURL: "m1sponsorlogourl",
		AdminName:      "m1adminname",
		AdminEmail:     "m1adminemail",
		CustomData:     "m1customdata",
		ContinentOnly:  true,
		CountryOnly:    false,
		ASOnly:         true,
		Score:          0,
		Latitude:       -20.0,
		Longitude:      55.0,
		ContinentCode:  "EU",
		CountryCodes:   "FR UK",
		Asnum:          444,
		Comment:        "m1comment",
		Enabled:        true,
		HttpUp:         true,
		HttpsUp:        true,
	}

	_, err := c.fetchMirror(testmirror.ID)
	if err == nil {
		t.Fatalf("Error expected, mock command not yet registered")
	}

	cmdGetMirror := mock.Command("HGETALL", "MIRROR_1").ExpectMap(map[string]string{
		"ID":            strconv.Itoa(testmirror.ID),
		"name":          testmirror.Name,
		"http":          testmirror.HttpURL,
		"rsync":         testmirror.RsyncURL,
		"ftp":           testmirror.FtpURL,
		"sponsorName":   testmirror.SponsorName,
		"sponsorURL":    testmirror.SponsorURL,
		"sponsorLogo":   testmirror.SponsorLogoURL,
		"adminName":     testmirror.AdminName,
		"adminEmail":    testmirror.AdminEmail,
		"customData":    testmirror.CustomData,
		"continentOnly": strconv.FormatBool(testmirror.ContinentOnly),
		"countryOnly":   strconv.FormatBool(testmirror.CountryOnly),
		"asOnly":        strconv.FormatBool(testmirror.ASOnly),
		"score":         strconv.FormatInt(int64(testmirror.Score), 10),
		"latitude":      fmt.Sprintf("%f", testmirror.Latitude),
		"longitude":     fmt.Sprintf("%f", testmirror.Longitude),
		"continentCode": testmirror.ContinentCode,
		"countryCodes":  testmirror.CountryCodes,
		"asnum":         strconv.FormatInt(int64(testmirror.Asnum), 10),
		"comment":       testmirror.Comment,
		"enabled":       strconv.FormatBool(testmirror.Enabled),
		"httpUp":        strconv.FormatBool(testmirror.HttpUp),
		"httpsUp":       strconv.FormatBool(testmirror.HttpsUp),
	})

	m, err := c.fetchMirror(testmirror.ID)
	if err != nil {
		t.Fatalf("Unexpected error: %s", err.Error())
	}

	if mock.Stats(cmdGetMirror) < 1 {
		t.Fatalf("HGETALL not executed")
	}

	// This is required to reach DeepEqual(ity)
	testmirror.Prepare()

	if !reflect.DeepEqual(testmirror, m) {
		t.Fatalf("Result is different")
	}

	_, ok := c.mCache.Get(strconv.Itoa(testmirror.ID))
	if !ok {
		t.Fatalf("Not stored in cache")
	}
}

func TestCache_fetchFileInfoMirror(t *testing.T) {
	mock, conn := PrepareRedisTest()
	conn.ConnectPubsub()

	c := NewCache(conn)

	testfile := filesystem.FileInfo{
		Path:    "/test/file.tgz",
		Size:    44000,
		ModTime: time.Now(),
		Sha1:    "3ce963aea2d6f23fe915063f8bba21888db0ddfa",
		Sha256:  "1c8e38c7e03e4d117eba4f82afaf6631a9b79f4c1e9dec144d4faf1d109aacda",
		Md5:     "2c98ec39f49da6ddd9cfa7b1d7342afe",
	}

	_, err := c.fetchFileInfoMirror(1, testfile.Path)
	if err == nil {
		t.Fatalf("Error expected, mock command not yet registered")
	}

	cmdGetFileinfomirror := mock.Command("HMGET", "FILEINFO_1_"+testfile.Path, "size", "modTime", "sha1", "sha256", "md5").Expect([]interface{}{
		[]byte(strconv.FormatInt(testfile.Size, 10)),
		[]byte(testfile.ModTime.String()),
		[]byte(testfile.Sha1),
		[]byte(testfile.Sha256),
		[]byte(testfile.Md5),
	})

	_, err = c.fetchFileInfoMirror(1, testfile.Path)
	if err != nil {
		t.Fatalf("Unexpected error: %s", err.Error())
	}

	if mock.Stats(cmdGetFileinfomirror) < 1 {
		t.Fatalf("HMGET not executed")
	}

	_, ok := c.fimCache.Get("1|" + testfile.Path)
	if !ok {
		t.Fatalf("Not stored in cache")
	}
}

func TestCache_GetMirror(t *testing.T) {
	mock, conn := PrepareRedisTest()
	conn.ConnectPubsub()

	c := NewCache(conn)

	testmirror := 1

	_, err := c.GetMirror(testmirror)
	if err == nil {
		t.Fatalf("Error expected, mock command not yet registered")
	}

	cmdGetMirror := mock.Command("HGETALL", "MIRROR_1").ExpectMap(map[string]string{
		"ID": strconv.Itoa(testmirror),
	})

	m, err := c.GetMirror(testmirror)
	if err != nil {
		t.Fatalf("Unexpected error: %s", err.Error())
	}

	if mock.Stats(cmdGetMirror) < 1 {
		t.Fatalf("HGETALL not executed")
	}

	// Results are already checked by TestCache_fetchMirror
	// We only need to check one of them
	if m.ID != testmirror {
		t.Fatalf("Result is different")
	}

	_, ok := c.mCache.Get(strconv.Itoa(testmirror))
	if !ok {
		t.Fatalf("Not stored in cache")
	}
}

func TestCache_GetMirrors(t *testing.T) {
	mock, conn := PrepareRedisTest()
	conn.ConnectPubsub()

	c := NewCache(conn)

	filename := "/test/file.tgz"

	clientInfo := network.GeoIPRecord{
		CountryCode: "FR",
		Latitude:    48.8567,
		Longitude:   2.3508,
	}

	_, err := c.GetMirrors(filename, clientInfo)
	if err == nil {
		t.Fatalf("Error expected, mock command not yet registered")
	}

	cmdGetFilemirrors := mock.Command("SMEMBERS", "FILEMIRRORS_"+filename).Expect([]interface{}{
		[]byte("1"),
		[]byte("2"),
	})

	cmdGetMirrorM1 := mock.Command("HGETALL", "MIRROR_1").ExpectMap(map[string]string{
		"ID":        "1",
		"latitude":  "52.5167",
		"longitude": "13.3833",
	})

	cmdGetMirrorM2 := mock.Command("HGETALL", "MIRROR_2").ExpectMap(map[string]string{
		"ID":        "2",
		"latitude":  "51.5072",
		"longitude": "0.1275",
	})

	cmdGetFileinfomirrorM1 := mock.Command("HMGET", "FILEINFO_1_"+filename, "size", "modTime", "sha1", "sha256", "md5").Expect([]interface{}{
		[]byte("44000"),
		[]byte(""),
		[]byte(""),
		[]byte(""),
		[]byte(""),
	})

	cmdGetFileinfomirrorM2 := mock.Command("HMGET", "FILEINFO_2_"+filename, "size", "modTime", "sha1", "sha256", "md5").Expect([]interface{}{
		[]byte("44000"),
		[]byte(""),
		[]byte(""),
		[]byte(""),
		[]byte(""),
	})

	mirrors, err := c.GetMirrors(filename, clientInfo)
	if err != nil {
		t.Fatalf("Unexpected error: %s", err.Error())
	}

	if mock.Stats(cmdGetFilemirrors) < 1 {
		t.Fatalf("cmdGetFilemirrors not called")
	}
	if mock.Stats(cmdGetMirrorM1) < 1 {
		t.Fatalf("cmdGetMirrorM1 not called")
	}
	if mock.Stats(cmdGetMirrorM2) < 1 {
		t.Fatalf("cmdGetMirrorM2 not called")
	}
	if mock.Stats(cmdGetFileinfomirrorM1) < 1 {
		t.Fatalf("cmdGetFileinfomirrorM1 not called")
	}
	if mock.Stats(cmdGetFileinfomirrorM2) < 1 {
		t.Fatalf("cmdGetFileinfomirrorM2 not called")
	}

	if len(mirrors) != 2 {
		t.Fatalf("Invalid number of mirrors returned")
	}

	if int(mirrors[0].Distance) != int(876) {
		t.Fatalf("Distance between user and m1 is wrong, got %d, expected 876", int(mirrors[0].Distance))
	}

	if int(mirrors[1].Distance) != int(334) {
		t.Fatalf("Distance between user and m2 is wrong, got %d, expected 334", int(mirrors[1].Distance))
	}
}
