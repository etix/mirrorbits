// Copyright (c) 2024 Arnaud Rebillout
// Licensed under the MIT license

package http

import (
	"fmt"
	"os"
	"testing"
	"time"

	. "github.com/etix/mirrorbits/config"
	"github.com/etix/mirrorbits/filesystem"
	"github.com/etix/mirrorbits/network"
	"github.com/etix/mirrorbits/mirrors"
)

var noFileInfo *filesystem.FileInfo
var noClientInfo network.GeoIPRecord

func TestMain(m *testing.M) {
	noFileInfo = nil
	noClientInfo = network.GeoIPRecord{}
	SetConfiguration(&Configuration{
		FixTimezoneOffsets: false,
	})
	os.Exit(m.Run())
}

// Helper to check the results of the Filter function on a single mirror
func checkResultsSingle(t *testing.T, a mirrors.Mirrors, x mirrors.Mirrors, reason string, url string) {
	t.Helper()
	var m mirrors.Mirror

	// If reason was set, we expect the mirror to have been excluded, while
	// no reason means that the mirror should have been accepted.
	if reason == "" {
		if len(a) != 1 || len(x) != 0 {
			t.Fatalf("There should be 1 mirror accepted and 0 mirror excluded")
		}
		m = a[0]
	} else {
		if len(a) != 0 || len(x) != 1 {
			t.Fatalf("There should be 0 mirror accepted and 1 mirror excluded")
		}
		m = x[0]
	}

	// Test that the field ExcludeReason was set as expected, or is unset
	// in case the mirror was accepted.
	if m.ExcludeReason != reason {
		t.Fatalf("Invalid ExcludeReason, expected '%s', got '%s'", reason, m.ExcludeReason)
	}

	// The field AbsoluteURL is expected to be set, except in the case when
	// the mirror is disabled.
	if m.ExcludeReason != "Disabled" && m.AbsoluteURL != url {
		t.Fatalf("Invalid AbsoluteURL, expected '%s', got '%s'", url, m.AbsoluteURL)
	}
}

// Helper to test the Filter function on a single mirror
func testFilterSingle(t *testing.T, m mirrors.Mirror, secureOption SecureOption, fileInfo *filesystem.FileInfo, clientInfo network.GeoIPRecord, reason string) {
	t.Helper()
	mlist := mirrors.Mirrors{m}
	a, x, _, _ := Filter(mlist, secureOption, fileInfo, clientInfo)
	checkResultsSingle(t, a, x, reason, m.HttpURL)
}

// Helper to test the Filter function on a single mirror, with a specific AbsoluteURL
func testFilterSingleAbsoluteURL(t *testing.T, m mirrors.Mirror, secureOption SecureOption, fileInfo *filesystem.FileInfo, clientInfo network.GeoIPRecord, reason string, url string) {
	t.Helper()
	mlist := mirrors.Mirrors{m}
	a, x, _, _ := Filter(mlist, secureOption, fileInfo, clientInfo)
	checkResultsSingle(t, a, x, reason, url)
}

func TestFilter(t *testing.T) {
	// Test that a mirror that is disabled is rejected

	m1 := mirrors.Mirror{
		HttpURL: "http://m1.mirror",
	}
	t.Run("disabled", func(t *testing.T) {
		testFilterSingle(t, m1, UNDEFINED, noFileInfo, noClientInfo, "Disabled")
	})

	// Given that a mirror is enabled, test that it's rejected when the
	// requested protocol is not available (either it's not supported by
	// the mirror, or it's down).

	tests1 := map[string]struct {
		secureOption SecureOption
		mirrorURL string
		excludeReason string
		absoluteURL string
	} {
		"want_https_but_http_only": {
			secureOption: WITHTLS,
			mirrorURL: "http://m1.mirror",
			excludeReason: "Not HTTPS",
			absoluteURL: "http://m1.mirror",
		},
		"want_https_has_https_but_down": {
			secureOption: WITHTLS,
			mirrorURL: "https://m1.mirror",
			excludeReason: "Down",
			absoluteURL: "https://m1.mirror",
		},
		"want_https_has_any_but_down": {
			secureOption: WITHTLS,
			mirrorURL: "m1.mirror",
			excludeReason: "Down",
			absoluteURL: "https://m1.mirror",
		},
		"want_http_but_https_only": {
			secureOption: WITHOUTTLS,
			mirrorURL: "https://m1.mirror",
			excludeReason: "Not HTTP",
			absoluteURL: "https://m1.mirror",
		},
		"want_http_has_http_but_down": {
			secureOption: WITHOUTTLS,
			mirrorURL: "http://m1.mirror",
			excludeReason: "Down",
			absoluteURL: "http://m1.mirror",
		},
		"want_http_has_any_but_down": {
			secureOption: WITHOUTTLS,
			mirrorURL: "m1.mirror",
			excludeReason: "Down",
			absoluteURL: "http://m1.mirror",
		},
		"want_any_has_http_only_but_down": {
			secureOption: UNDEFINED,
			mirrorURL: "http://m1.mirror",
			excludeReason: "Down / Not HTTPS",
			absoluteURL: "http://m1.mirror",
		},
		"want_any_has_https_only_but_down": {
			secureOption: UNDEFINED,
			mirrorURL: "https://m1.mirror",
			excludeReason: "Not HTTP / Down",
			absoluteURL: "https://m1.mirror",
		},
		"want_any_has_any_but_down": {
			secureOption: UNDEFINED,
			mirrorURL: "m1.mirror",
			excludeReason: "Down",
			absoluteURL: "http://m1.mirror",
		},
	}

	for name, test := range tests1 {
		m1 := mirrors.Mirror{
			HttpURL: test.mirrorURL,
			Enabled: true,
		}
		t.Run(name, func(t *testing.T) {
			testFilterSingleAbsoluteURL(t, m1, test.secureOption, noFileInfo, noClientInfo,
				test.excludeReason, test.absoluteURL)
		})
	}

	// Given that a mirror is enabled and the protocol requested is available,
	// test that a mirror is rejected when the requested file is not valid
	// (wrong size or mod time).

	testfile := &filesystem.FileInfo{
		Path: "/test/file.tgz",
		Size: 43000,
		ModTime: time.Now(),
	}

	tests2 := map[string]struct {
		fileSize int64
		fileModTime time.Time
		excludeReason string
	} {
		"wrong_size": {
			fileSize: 12345,
			fileModTime: testfile.ModTime,
			excludeReason: "File size mismatch",
		},
		"wrong_mod_time_newer_on_mirror": {
			fileSize: testfile.Size,
			fileModTime: testfile.ModTime.Add(time.Second * 10),
			excludeReason: "Mod time mismatch (diff: -10s)",
		},
		"wrong_mod_time_older_on_mirror": {
			fileSize: testfile.Size,
			fileModTime: testfile.ModTime.Add(time.Second * -10),
			excludeReason: "Mod time mismatch (diff: 10s)",
		},
	}

	for name, test := range tests2 {
		m1 := mirrors.Mirror{
			HttpURL: "https://m1.mirror",
			Enabled: true,
			HttpsUp: true,
			FileInfo: &filesystem.FileInfo{
				Path: "/test/file.tgz",
				Size: test.fileSize,
				ModTime: test.fileModTime,
			},
		}
		t.Run(name, func(t *testing.T) {
			testFilterSingle(t, m1, WITHTLS, testfile, noClientInfo, test.excludeReason)
		})
	}

	// Given that a mirror is enabled, the protocol requested is available
	// and the file on the mirror is valid, test that a mirror is rejected
	// when the client doesn't meet the geolocation requirements.

	clientInfo := network.GeoIPRecord{
		ContinentCode: "EU",
		CountryCode: "FR",
		ASNum: 4444,
	}

	tests3 := map[string]struct {
		continentOnly bool
		continentCode string
		countryOnly bool
		countryCodes string
		asOnly bool
		asNum uint
		excludedCountryCodes string
		excludeReason string
	} {
		"wrong_continent": {
			continentOnly: true,
			continentCode: "NA",
			excludeReason: "Continent only",
		},
		"wrong_country": {
			countryOnly: true,
			countryCodes: "UK",
			excludeReason: "Country only",
		},
		"wrong_countries": {
			countryOnly: true,
			countryCodes: "FI NO SE",
			excludeReason: "Country only",
		},
		"wrong_as": {
			asOnly: true,
			asNum: 5555,
			excludeReason: "AS only",
		},
		"excluded_country": {
			excludedCountryCodes: "FR",
			excludeReason: "User's country restriction",
		},
		"excluded_countries": {
			excludedCountryCodes: "ES FR IT PT",
			excludeReason: "User's country restriction",
		},
	}

	for name, test := range tests3 {
		m1 := mirrors.Mirror{
			HttpURL: "https://m1.mirror",
			Enabled: true,
			HttpsUp: true,
			FileInfo: testfile,
			ContinentOnly: test.continentOnly,
			ContinentCode: test.continentCode,
			CountryOnly: test.countryOnly,
			CountryCodes: test.countryCodes,
			ASOnly: test.asOnly,
			Asnum: test.asNum,
			ExcludedCountryCodes: test.excludedCountryCodes,
		}
		m1.Prepare()
		t.Run(name, func(t *testing.T) {
			testFilterSingle(t, m1, WITHTLS, testfile, clientInfo, test.excludeReason)
		})
	}

	// Given valid mirrors, test that the distances returned are correct.

	tests4 := map[string]struct {
		distances []float32
		extrema []float32
	} {
		"no_mirror": {
			distances: []float32{},
			extrema: []float32{0, 0},
		},
		"one_mirror": {
			distances: []float32{10},
			extrema: []float32{10, 10},
		},
		"some_mirrors": {
			distances: []float32{30, 20, 10},
			extrema: []float32{10, 30},
		},
	}

	for name, test := range tests4 {
		mlist := make([]mirrors.Mirror, 0, 5)
		for i, d := range test.distances {
			m := mirrors.Mirror{
				HttpURL: fmt.Sprintf("https://m%d.mirror", i),
				Enabled: true,
				HttpsUp: true,
				FileInfo: testfile,
				Distance: d,
			}
			mlist = append(mlist, m)
		}
		t.Run(name, func(t *testing.T) {
			a, x, closest, farthest := Filter(mlist, WITHTLS, testfile, clientInfo)
			if len(a) != len(mlist) || len(x) != 0 {
				t.Fatalf("There should be %d mirror(s) accepted and 0 mirror excluded",
					len(mlist))
			}
			if closest != test.extrema[0] || farthest != test.extrema[1] {
				t.Fatalf("Wrong results for [closest farthest], expected %v, got %v",
					test.extrema, []float32{closest, farthest})
			}
		})
	}
}

func TestFilterAllowOutdatedFiles(t *testing.T) {
	// Given a file that is outdated on a mirror, test that the mirror is
	// rejected, unless the configuration setting AllowOutdatedFiles is set
	// correctly in order to accept this file.

	testfile := &filesystem.FileInfo{
		Path: "/test/file.tgz",
		Size: 43000,
		ModTime: time.Now(),
	}

	configValues := [][]OutdatedFilesConfig{
		[]OutdatedFilesConfig{},
		[]OutdatedFilesConfig{{
			Prefix: "/test/",
			Minutes: 1,
		}},
		[]OutdatedFilesConfig{{
			Prefix: "/wrong/",
			Minutes: 2,
		}},
		[]OutdatedFilesConfig{{
			Prefix: "/test/",
			Minutes: 2,
		}},
	}

	tests := map[string]struct {
		fileSize int64
		fileModTime time.Time
		excludeReason []string
	} {
		"outdated_same_size": {
			fileSize: testfile.Size,
			fileModTime: testfile.ModTime.Add(-100 * time.Second),
			excludeReason: []string{
				"Mod time mismatch (diff: 1m40s)",
				"Mod time mismatch (diff: 1m40s)",
				"Mod time mismatch (diff: 1m40s)",
				"",
			},
		},
		"outdated_different_size": {
			fileSize: 12345,
			fileModTime: testfile.ModTime.Add(-100 * time.Second),
			excludeReason: []string{
				"File size mismatch",
				"Mod time mismatch (diff: 1m40s)",
				"File size mismatch",
				"",
			},
		},
	}

	for idx, configValue := range configValues {
		SetConfiguration(&Configuration{
			AllowOutdatedFiles: configValue,
		})
		for name, test := range tests {
			m1 := mirrors.Mirror{
				HttpURL: fmt.Sprintf("https://m%d.mirror", 1),
				Enabled: true,
				HttpsUp: true,
				FileInfo: &filesystem.FileInfo{
					Path: testfile.Path,
					Size: test.fileSize,
					ModTime: test.fileModTime,
				},
			}
			t.Run(name, func(t *testing.T) {
				testFilterSingle(t, m1, WITHTLS, testfile, noClientInfo, test.excludeReason[idx])
			})
		}
	}
}

func TestFilterFixTimezoneOffsets(t *testing.T) {
	// Given a mirror with a 1-hour timezone offset, test that the mirror
	// is rejected unless 1) the TZOffset of the mirror is set correctly,
	// and 2) the configuration setting FixTimezoneOffsets is enabled.

	var offset int64 = 3600
	modTime := time.Now()
	outdatedModTime := modTime.Add(time.Duration(-offset) * time.Second)

	fileRequested := &filesystem.FileInfo{
		Path: "/test/file.tgz",
		Size: 43000,
		ModTime: modTime,
	}

	fileOnMirror := &filesystem.FileInfo{
		Path: "/test/file.tgz",
		Size: 43000,
		ModTime: outdatedModTime,
	}

	configValues := []bool{false, true}

	tests := map[string]struct {
		tzoffset int64
		excludeReason []string
		
	} {
		"tzoffset_unset": {
			tzoffset: 0,
			excludeReason: []string{
				"Mod time mismatch (diff: 1h0m0s)",
				"Mod time mismatch (diff: 1h0m0s)",
			},
		},
		"tzoffset_set": {
			tzoffset: offset * 1000,
			excludeReason: []string{
				"Mod time mismatch (diff: 1h0m0s)",
				"",
			},
		},
	}

	for idx, configValue := range configValues {
		SetConfiguration(&Configuration{
			FixTimezoneOffsets: configValue,
		})
		for name, test := range tests {
			m1 := mirrors.Mirror{
				HttpURL: fmt.Sprintf("https://m%d.mirror", 1),
				Enabled: true,
				HttpsUp: true,
				FileInfo: fileOnMirror,
				TZOffset: test.tzoffset,
			}
			t.Run(name, func(t *testing.T) {
				testFilterSingle(t, m1, WITHTLS, fileRequested, noClientInfo, test.excludeReason[idx])
			})
		}
	}
}
