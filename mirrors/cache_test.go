// Copyright (c) 2014-2017 Ludovic Fauvet
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
	"github.com/garyburd/redigo/redis"
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

	if f.Path != testfile.Path {
		t.Fatalf("Path doesn't match, expected %#v got %#v", testfile.Path, f.Path)
	}
	if f.Size != testfile.Size {
		t.Fatalf("Size doesn't match, expected %#v got %#v", testfile.Size, f.Size)
	}
	if !f.ModTime.Equal(testfile.ModTime) {
		t.Fatalf("ModTime doesn't match, expected %s got %s", testfile.ModTime.String(), f.ModTime.String())
	}
	if f.Sha1 != testfile.Sha1 {
		t.Fatalf("Sha1 doesn't match, expected %#v got %#v", testfile.Sha1, f.Sha1)
	}
	if f.Sha256 != testfile.Sha256 {
		t.Fatalf("Sha256 doesn't match, expected %#v got %#v", testfile.Sha256, f.Sha256)
	}
	if f.Md5 != testfile.Md5 {
		t.Fatalf("Md5 doesn't match, expected %#v got %#v", testfile.Md5, f.Md5)
	}

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

	// Results are already checked by TestCache_fetchFileInfo
	// We only need to check one of them
	if !f.ModTime.Equal(testfile.ModTime) {
		t.Fatalf("One or more values do not match")
	}

	_, err = c.GetFileInfo(testfile.Path)
	if err == redis.ErrNil {
		t.Fatalf("Cache not used, request expected to be done once")
	} else if err != nil {
		t.Fatalf("Unexpected error: %s", err.Error())
	}
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
		[]byte("m9"),
		[]byte("m2"),
		[]byte("m5"),
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
		ID:             "m1",
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
		Up:             true,
	}

	_, err := c.fetchMirror(testmirror.ID)
	if err == nil {
		t.Fatalf("Error expected, mock command not yet registered")
	}

	cmdGetMirror := mock.Command("HGETALL", "MIRROR_m1").ExpectMap(map[string]string{
		"ID":            testmirror.ID,
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
		"up":            strconv.FormatBool(testmirror.Up),
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

	_, ok := c.mCache.Get(testmirror.ID)
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

	_, err := c.fetchFileInfoMirror("m1", testfile.Path)
	if err == nil {
		t.Fatalf("Error expected, mock command not yet registered")
	}

	cmdGetFileinfomirror := mock.Command("HMGET", "FILEINFO_m1_"+testfile.Path, "size", "modTime", "sha1", "sha256", "md5").ExpectMap(map[string]string{
		"size":    strconv.FormatInt(testfile.Size, 10),
		"modTime": testfile.ModTime.String(),
		"sha1":    testfile.Sha1,
		"sha256":  testfile.Sha256,
		"md5":     testfile.Md5,
	})

	_, err = c.fetchFileInfoMirror("m1", testfile.Path)
	if err != nil {
		t.Fatalf("Unexpected error: %s", err.Error())
	}

	if mock.Stats(cmdGetFileinfomirror) < 1 {
		t.Fatalf("HGETALL not executed")
	}

	_, ok := c.fimCache.Get("m1|" + testfile.Path)
	if !ok {
		t.Fatalf("Not stored in cache")
	}
}

func TestCache_GetMirror(t *testing.T) {
	mock, conn := PrepareRedisTest()
	conn.ConnectPubsub()

	c := NewCache(conn)

	testmirror := "m1"

	_, err := c.GetMirror(testmirror)
	if err == nil {
		t.Fatalf("Error expected, mock command not yet registered")
	}

	cmdGetMirror := mock.Command("HGETALL", "MIRROR_m1").ExpectMap(map[string]string{
		"ID": testmirror,
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

	_, ok := c.mCache.Get(testmirror)
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
		[]byte("m1"),
		[]byte("m2"),
	})

	cmdGetMirrorM1 := mock.Command("HGETALL", "MIRROR_m1").ExpectMap(map[string]string{
		"ID":        "m1",
		"latitude":  "52.5167",
		"longitude": "13.3833",
	})

	cmdGetMirrorM2 := mock.Command("HGETALL", "MIRROR_m2").ExpectMap(map[string]string{
		"ID":        "m2",
		"latitude":  "51.5072",
		"longitude": "0.1275",
	})

	cmdGetFileinfomirrorM1 := mock.Command("HMGET", "FILEINFO_m1_"+filename, "size", "modTime", "sha1", "sha256", "md5").ExpectMap(map[string]string{
		"size":    "44000",
		"modTime": "",
		"sha1":    "",
		"sha256":  "",
		"md5":     "",
	})

	cmdGetFileinfomirrorM2 := mock.Command("HMGET", "FILEINFO_m2_"+filename, "size", "modTime", "sha1", "sha256", "md5").ExpectMap(map[string]string{
		"size":    "44000",
		"modTime": "",
		"sha1":    "",
		"sha256":  "",
		"md5":     "",
	})

	mirrors, err := c.GetMirrors(filename, clientInfo)
	if err != nil {
		t.Fatalf("Unexpected error: %s", err.Error())
	}

	if mock.Stats(cmdGetFilemirrors) < 1 {
		t.Fatalf("cmd_get_filemirrors not called")
	}
	if mock.Stats(cmdGetMirrorM1) < 1 {
		t.Fatalf("cmdGetMirrorM1 not called")
	}
	if mock.Stats(cmdGetMirrorM2) < 1 {
		t.Fatalf("cmdGetMirrorM2 not called")
	}
	if mock.Stats(cmdGetFileinfomirrorM1) < 1 {
		t.Fatalf("cmd_get_fileinfomirror_m1 not called")
	}
	if mock.Stats(cmdGetFileinfomirrorM2) < 1 {
		t.Fatalf("cmd_get_fileinfomirror_m2 not called")
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
