// Copyright (c) 2014-2015 Ludovic Fauvet
// Licensed under the MIT license

package network

import (
	"github.com/etix/geoip"
	"testing"
	"time"
)

func TestNewGeoIP(t *testing.T) {
	g := NewGeoIP()
	if g == nil {
		t.Fatalf("Expected valid pointer, got nil")
	}
}

func TestGeoIP_GetRecord(t *testing.T) {
	g := NewGeoIP()

	mockv4 := &geoipDB{
		filename: "testv4.dat",
		modTime:  time.Now(),
		db:       &GeoIPMockV4{},
	}

	mockv6 := &geoipDB{
		filename: "testv4.dat",
		modTime:  time.Now(),
		db:       &GeoIPMockV6{},
	}

	g.geo = mockv4
	g.geo6 = mockv6
	g.asn = mockv4
	g.asn6 = mockv6

	/* ipv4 */
	r := g.GetRecord("127.0.0.1")
	if r.CountryName != "IPV4" {
		t.Fatalf("Expected IPV4 got %s", r.CountryName)
	}
	if r.ASNum != 4444 {
		t.Fatalf("Expected 4 got %d", r.ASNum)
	}
	if r.ASName != "IPV4" {
		t.Fatalf("Expected IPV4 got %d", r.ASName)
	}
	if r.IsValid() == false {
		t.Fatalf("Expected valid got invalid")
	}

	/* ipv6 */
	r = g.GetRecord("::1")
	if r.CountryName != "IPV6" {
		t.Fatalf("Expected IPV6 got %s", r.CountryName)
	}
	if r.ASNum != 6666 {
		t.Fatalf("Expected 6 got %d", r.ASNum)
	}
	if r.ASName != "IPV6" {
		t.Fatalf("Expected IPV6 got %d", r.ASName)
	}
	if r.IsValid() == false {
		t.Fatalf("Expected valid got invalid")
	}
}

func TestIsIPv6(t *testing.T) {
	g := NewGeoIP()
	if g.IsIPv6("192.168.0.1") == true {
		t.Fatalf("Expected ipv4, got ipv6")
	}
	if g.IsIPv6("::1") == false {
		t.Fatalf("Expected ipv6, got ipv4")
	}
	if g.IsIPv6("fe80::801a:2cff:fe80:315c") == false {
		t.Fatalf("Expected ipv6, got ipv4")
	}
}

func TestGeoIPRecord_IsValid(t *testing.T) {
	var r GeoIPRecord

	if r.IsValid() == true {
		t.Fatalf("Expected false, got true")
	}

	r.GeoIPRecord = &geoip.GeoIPRecord{}

	if r.IsValid() == false {
		t.Fatalf("Expected true, got false")
	}
}

/* MOCK: github.com/etix/geoip */

type GeoIPMockV4 struct {
}

func (g *GeoIPMockV4) GetRecord(ip string) *geoip.GeoIPRecord {
	return &geoip.GeoIPRecord{
		CountryName: "IPV4", // fake
	}
}

func (g *GeoIPMockV4) GetName(ip string) (name string, netmask int) {
	return "AS4444 IPV4", 4
}

type GeoIPMockV6 struct {
}

func (g *GeoIPMockV6) GetRecord(ip string) *geoip.GeoIPRecord {
	return &geoip.GeoIPRecord{
		CountryName: "IPV6", // fake
	}
}

func (g *GeoIPMockV6) GetName(ip string) (name string, netmask int) {
	return "AS6666 IPV6", 6
}
