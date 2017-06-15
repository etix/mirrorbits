// Copyright (c) 2014-2017 Ludovic Fauvet
// Licensed under the MIT license

package utils

import (
	"testing"
	"time"

	"github.com/etix/mirrorbits/core"
	"github.com/etix/mirrorbits/network"
)

func TestNormalizeURL(t *testing.T) {
	s := []string{
		"", "",
		"rsync://test.com", "rsync://test.com/",
		"rsync://test.com/", "rsync://test.com/",
	}

	if len(s)%2 != 0 {
		t.Fatal("not multiple of 2")
	}

	for i := 0; i < len(s); i += 2 {
		if r := NormalizeURL(s[i]); r != s[i+1] {
			t.Fatalf("%q: expected %q, got %q", s[i], s[i+1], r)
		}
	}
}

func TestGetDistanceKm(t *testing.T) {
	if r := GetDistanceKm(48.8567, 2.3508, 40.7127, 74.0059); int(r) != 5514 {
		t.Fatalf("Expected 5514, got %f", r)
	}
	if r := GetDistanceKm(48.8567, 2.3508, 48.8567, 2.3508); int(r) != 0 {
		t.Fatalf("Expected 0, got %f", r)
	}
}

func TestMin(t *testing.T) {
	if r := Min(-10, 5); r != -10 {
		t.Fatalf("Expected -10, got %d", r)
	}
}

func TestMax(t *testing.T) {
	if r := Max(-10, 5); r != 5 {
		t.Fatalf("Expected 5, got %d", r)
	}
}

func TestAdd(t *testing.T) {
	if r := Add(2, 40); r != 42 {
		t.Fatalf("Expected 42, got %d", r)
	}
}

func TestVersion(t *testing.T) {
	if r := Version(); len(r) == 0 || r != core.VERSION {
		t.Fatalf("Expected %s, got %s", core.VERSION, r)
	}
}

func TestHostname(t *testing.T) {
	if r := Hostname(); len(r) == 0 {
		t.Fatalf("Expected a valid hostname")
	}
}

func TestIsInSlice(t *testing.T) {
	var b bool
	list := []string{"aaa", "bbb", "ccc"}

	b = IsInSlice("ccc", list)
	if !b {
		t.Fatal("Expected true, got false")
	}
	b = IsInSlice("b", list)
	if b {
		t.Fatal("Expected false, got true")
	}
	b = IsInSlice("", list)
	if b {
		t.Fatal("Expected false, got true")
	}
}

func TestIsAdditionalCountry(t *testing.T) {
	var b bool
	list := []string{"FR", "DE", "GR"}

	clientInfo := network.GeoIPRecord{
		CountryCode: "FR",
	}

	b = IsAdditionalCountry(clientInfo, list)
	if b {
		t.Fatal("Expected false, got true")
	}

	clientInfo = network.GeoIPRecord{
		CountryCode: "GR",
	}

	b = IsAdditionalCountry(clientInfo, list)
	if !b {
		t.Fatal("Expected true, got false")
	}
}

func TestIsPrimaryCountry(t *testing.T) {
	var b bool
	list := []string{"FR", "DE", "GR"}

	clientInfo := network.GeoIPRecord{
		CountryCode: "FR",
	}

	b = IsPrimaryCountry(clientInfo, list)
	if !b {
		t.Fatal("Expected true, got false")
	}

	clientInfo = network.GeoIPRecord{
		CountryCode: "GR",
	}

	b = IsPrimaryCountry(clientInfo, list)
	if b {
		t.Fatal("Expected false, got true")
	}
}

func TestIsStopped(t *testing.T) {
	stop := make(chan bool, 1)

	if IsStopped(stop) {
		t.Fatal("Expected false, got true")
	}

	stop <- true

	if !IsStopped(stop) {
		t.Fatal("Expected true, got false")
	}
}

func TestReadableSize(t *testing.T) {
	ivalues := []int64{0, 1, 1024, 1000000}
	svalues := []string{"0.0 bytes", "1.0 bytes", "1.0 KB", "976.6 KB"}

	for i := range ivalues {
		if r := ReadableSize(ivalues[i]); r != svalues[i] {
			t.Fatalf("Expected %q, got %q", svalues[i], r)
		}
	}
}

func TestElapsedSec(t *testing.T) {
	now := time.Now().UTC().Unix()

	lastTimestamp := now - 1000

	if ElapsedSec(lastTimestamp, 500) == false {
		t.Fatalf("Expected true, got false")
	}
	if ElapsedSec(lastTimestamp, 5000) == true {
		t.Fatalf("Expected false, got true")
	}
}

func TestPlural(t *testing.T) {
	if Plural(2) != "s" {
		t.Fatalf("Expected 's', got ''")
	}
	if Plural(10000000) != "s" {
		t.Fatalf("Expected 's', got ''")
	}
	if Plural(-2) != "s" {
		t.Fatalf("Expected 's', got ''")
	}
	if Plural(1) != "" {
		t.Fatalf("Expected '', got 's'")
	}
	if Plural(-1) != "" {
		t.Fatalf("Expected '', got 's'")
	}
	if Plural(0) != "" {
		t.Fatalf("Expected '', got 's'")
	}
}

func TestConcatURL(t *testing.T) {
	part1 := "http://test.example/somedir/"
	part2 := "/somefile.bin"
	result := "http://test.example/somedir/somefile.bin"
	if r := ConcatURL(part1, part2); r != result {
		t.Fatalf("Expected %s, got %s", result, r)
	}

	part1 = "http://test.example/somedir"
	part2 = "/somefile.bin"
	result = "http://test.example/somedir/somefile.bin"
	if r := ConcatURL(part1, part2); r != result {
		t.Fatalf("Expected %s, got %s", result, r)
	}

	part1 = "http://test.example/somedir"
	part2 = "somefile.bin"
	result = "http://test.example/somedir/somefile.bin"
	if r := ConcatURL(part1, part2); r != result {
		t.Fatalf("Expected %s, got %s", result, r)
	}
}

func TestTimeKeyCoverage(t *testing.T) {
	date1Start := time.Date(2015, 10, 30, 12, 42, 11, 0, time.UTC)
	date1End := time.Date(2015, 12, 2, 13, 42, 11, 0, time.UTC)
	result1 := []string{"2015_10_30", "2015_10_31", "2015_11", "2015_12_01"}

	result := TimeKeyCoverage(date1Start, date1End)

	if len(result) != len(result1) {
		t.Fatalf("Expect %d elements, got %d", len(result1), len(result))
	}

	for i, r := range result {
		if r != result1[i] {
			t.Fatalf("Expect %#v, got %#v", result1, result)
		}
	}

	/* */

	date2Start := time.Date(2015, 12, 2, 12, 42, 11, 0, time.UTC)
	date2End := time.Date(2015, 12, 2, 13, 42, 11, 0, time.UTC)
	result2 := []string{"2015_12_02"}

	result = TimeKeyCoverage(date2Start, date2End)

	if len(result) != len(result2) {
		t.Fatalf("Expect %d elements, got %d", len(result2), len(result))
	}

	for i, r := range result {
		if r != result2[i] {
			t.Fatalf("Expect %#v, got %#v", result2, result)
		}
	}

	/* */

	date3Start := time.Date(2015, 1, 1, 12, 42, 11, 0, time.UTC)
	date3End := time.Date(2017, 1, 1, 13, 42, 11, 0, time.UTC)
	result3 := []string{"2015", "2016"}

	result = TimeKeyCoverage(date3Start, date3End)

	if len(result) != len(result3) {
		t.Fatalf("Expect %d elements, got %d", len(result3), len(result))
	}

	for i, r := range result {
		if r != result3[i] {
			t.Fatalf("Expect %#v, got %#v", result3, result)
		}
	}

	/* */

	date4Start := time.Date(2015, 12, 31, 12, 42, 11, 0, time.UTC)
	date4End := time.Date(2016, 1, 2, 13, 42, 11, 0, time.UTC)
	result4 := []string{"2015_12_31", "2016_01_01"}

	result = TimeKeyCoverage(date4Start, date4End)

	if len(result) != len(result4) {
		t.Fatalf("Expect %d elements, got %d", len(result4), len(result))
	}

	for i, r := range result {
		if r != result4[i] {
			t.Fatalf("Expect %#v, got %#v", result4, result)
		}
	}
}
