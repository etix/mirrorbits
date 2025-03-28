// Copyright (c) 2025 Arnaud Rebillout
// Licensed under the MIT license

package database

import (
	"fmt"
	"testing"
)

func TestIsAtLeastVersion(t *testing.T) {
	testsFalse := [] struct {
		have string
		want string
	} {
		{"",      "3.2.0"},
		{"2.0",   "3.2.0"},
		{"2.0.0", "3.2"},
		{"2.0.0", "3.2.0"},
	}
	for i, test := range testsFalse {
		r := Redis{
			version: test.have,
		}
		t.Run(fmt.Sprintf("testsFalse/%d", i), func(t *testing.T) {
			if r.IsAtLeastVersion(test.want) {
				t.Errorf("Expected '%s' < '%s'", test.have, test.want)
			}
		})
	}

	testsTrue := [] struct {
		have string
		want string
	} {
		{"3.2",   "3.2.0"},
		{"3.2.0", "3.2.0"},
		{"6.0",   "3.2.0"},
		{"6.0.0", "3.2"},
		{"6.0.0", "3.2.0"},
	}
	for i, test := range testsTrue {
		r := Redis{
			version: test.have,
		}
		t.Run(fmt.Sprintf("testsTrue/%d", i), func(t *testing.T) {
			if ! r.IsAtLeastVersion(test.want) {
				t.Errorf("Expected '%s' >= '%s'", test.have, test.want)
			}
		})
	}
}
