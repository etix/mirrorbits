// Copyright (c) 2014-2019 Ludovic Fauvet
// Licensed under the MIT license

package network

import (
	"testing"
)

func TestRemoteIpFromAddr(t *testing.T) {
	r := RemoteIPFromAddr("127.0.0.1:8080")
	if r != "127.0.0.1" {
		t.Fatalf("Expected '127.0.0.1', got %s", r)
	}

	r = RemoteIPFromAddr("[::1]:8080")
	if r != "[::1]" {
		t.Fatalf("Expected '[::1]', got %s", r)
	}

	r = RemoteIPFromAddr(":8080")
	if r != "" {
		t.Fatalf("Expected '', got %s", r)
	}
}

func TestExtractRemoteIP(t *testing.T) {
	r := ExtractRemoteIP("192.168.0.1, 192.168.0.2, 192.168.0.3")
	if r != "192.168.0.1" {
		t.Fatalf("Expected '192.168.0.1', got %s", r)
	}

	r = ExtractRemoteIP("192.168.0.1,192.168.0.2,192.168.0.3")
	if r != "192.168.0.1" {
		t.Fatalf("Expected '192.168.0.1', got %s", r)
	}
}

func TestIsPrimaryCountry(t *testing.T) {
	var b bool
	list := []string{"FR", "DE", "GR"}

	clientInfo := GeoIPRecord{
		CountryCode: "FR",
	}

	b = IsPrimaryCountry(clientInfo, list)
	if !b {
		t.Fatal("Expected true, got false")
	}

	clientInfo = GeoIPRecord{
		CountryCode: "GR",
	}

	b = IsPrimaryCountry(clientInfo, list)
	if b {
		t.Fatal("Expected false, got true")
	}
}

func TestIsAdditionalCountry(t *testing.T) {
	var b bool
	list := []string{"FR", "DE", "GR"}

	clientInfo := GeoIPRecord{
		CountryCode: "FR",
	}

	b = IsAdditionalCountry(clientInfo, list)
	if b {
		t.Fatal("Expected false, got true")
	}

	clientInfo = GeoIPRecord{
		CountryCode: "GR",
	}

	b = IsAdditionalCountry(clientInfo, list)
	if !b {
		t.Fatal("Expected true, got false")
	}
}
