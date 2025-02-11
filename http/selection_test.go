// Copyright (c) 2024 Arnaud Rebillout
// Licensed under the MIT license

package http

import (
	"fmt"
	"testing"
	"time"

	. "github.com/etix/mirrorbits/config"
	"github.com/etix/mirrorbits/filesystem"
	"github.com/etix/mirrorbits/network"
	"github.com/etix/mirrorbits/mirrors"
)

func TestFilter(t *testing.T) {
	// Test that a mirror that is disabled is rejected

	m1 := mirrors.Mirror{
		HttpURL: "http://m1.mirror",
	}
	mlist := mirrors.Mirrors{m1}
	a, x, _, _ := Filter(mlist, UNDEFINED, nil, network.GeoIPRecord{})
	if len(a) != 0 || len(x) != 1 {
		t.Fatalf("There should be 0 mirror accepted and 1 mirror excluded")
	}
	if m := x[0]; m.ExcludeReason != "Disabled" {
		t.Fatalf("Invalid ExcludeReason, expected '%s', got '%s'",
			"Disabled", m.ExcludeReason)
	}

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
		mlist := mirrors.Mirrors{m1}
		t.Run(name, func(t *testing.T) {
			a, x, _, _ := Filter(mlist, test.secureOption, nil, network.GeoIPRecord{})
			if len(a) != 0 || len(x) != 1 {
				t.Fatalf("There should be 0 mirror accepted and 1 mirror excluded")
			}
			if m := x[0]; m.ExcludeReason != test.excludeReason {
				t.Fatalf("Invalid ExcludeReason, expected '%s', got '%s'",
					test.excludeReason, m.ExcludeReason)
			}
			if m := x[0]; m.AbsoluteURL != test.absoluteURL {
				t.Fatalf("Invalid AbsoluteURL, expected '%s', got '%s'",
					test.absoluteURL, m.AbsoluteURL)
			}
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
		"wrong_mod_time": {
			fileSize: testfile.Size,
			fileModTime: testfile.ModTime.Add(time.Second * 10),
			excludeReason: "Mod time mismatch (diff: -10s)",
		},
	}

	SetConfiguration(&Configuration{
		FixTimezoneOffsets: false,
	})

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
		mlist := mirrors.Mirrors{m1}
		t.Run(name, func(t *testing.T) {
			a, x, _, _ := Filter(mlist, WITHTLS, testfile, network.GeoIPRecord{})
			if len(a) != 0 || len(x) != 1 {
				t.Fatalf("There should be 0 mirror accepted and 1 mirror excluded")
			}
			if m := x[0]; m.ExcludeReason != test.excludeReason {
				t.Fatalf("Invalid ExcludeReason, expected '%s', got '%s'",
					test.excludeReason, m.ExcludeReason)
			}
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
		mlist := mirrors.Mirrors{m1}
		t.Run(name, func(t *testing.T) {
			a, x, _, _ := Filter(mlist, WITHTLS, testfile, clientInfo)
			if len(a) != 0 || len(x) != 1 {
				t.Fatalf("There should be 0 mirror accepted and 1 mirror excluded")
			}
			if m := x[0]; m.ExcludeReason != test.excludeReason {
				t.Fatalf("Invalid ExcludeReason, expected '%s', got '%s'",
					test.excludeReason, m.ExcludeReason)
			}
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
