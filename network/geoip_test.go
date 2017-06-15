// Copyright (c) 2014-2017 Ludovic Fauvet
// Licensed under the MIT license

package network

import (
	"net"
	"reflect"
	"strings"
	"testing"
	"time"
)

type CityDb struct {
	City struct {
		Names struct {
			En string
		}
	}
	Country struct {
		Iso_Code string
		Names    struct {
			En string
		}
	}
	Continent struct {
		Code string
	}
	Location struct {
		Latitude  float64
		Longitude float64
	}
}

type ASNDb struct {
	Autonomous_system_number       uint
	Autonomous_system_organization string
}

func TestNewGeoIP(t *testing.T) {
	g := NewGeoIP()
	if g == nil {
		t.Fatalf("Expected valid pointer, got nil")
	}
}

func TestGeoIP_GetRecord(t *testing.T) {
	g := NewGeoIP()

	mockcity := &geoipDB{
		filename: "city.mmdb",
		modTime:  time.Now(),
		db:       &GeoIPMockCity{},
	}

	mockasn := &geoipDB{
		filename: "asn.mmdb",
		modTime:  time.Now(),
		db:       &GeoIPMockASN{},
	}

	g.city = mockcity
	g.asn = mockasn

	/* city */
	r := g.GetRecord("127.0.0.1")
	if r.City != "test1" {
		t.Fatalf("Invalid response got %s, expected test1", r.City)
	}
	if r.CountryCode != "test2" {
		t.Fatalf("Invalid response got %s, expected test2", r.CountryCode)
	}
	if r.Country != "test3" {
		t.Fatalf("Invalid response got %s, expected test3", r.Country)
	}
	if r.ContinentCode != "test4" {
		t.Fatalf("Invalid response got %s, expected test4", r.ContinentCode)
	}
	if r.Latitude != 24 {
		t.Fatalf("Invalid response got %f, expected 24", r.Latitude)
	}
	if r.Longitude != 42 {
		t.Fatalf("Invalid response got %f, expected 42", r.Longitude)
	}
	if r.ASNum != 42 {
		t.Fatalf("Invalid response got %d, expected 42", r.ASNum)
	}
	if r.ASName != "forty two" {
		t.Fatalf("Invalid response got %s, expected forty two", r.ASName)
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

	r = GeoIPRecord{
		CountryCode: "FR",
	}

	if r.IsValid() == false {
		t.Fatalf("Expected true, got false")
	}
}

/* MOCK */

type GeoIPMockCity struct {
}

func (g *GeoIPMockCity) Lookup(ipAddress net.IP, result interface{}) error {
	var citydb CityDb
	citydb.City.Names.En = "test1"
	citydb.Country.Iso_Code = "test2"
	citydb.Country.Names.En = "test3"
	citydb.Continent.Code = "test4"
	citydb.Location.Latitude = 24
	citydb.Location.Longitude = 42

	CopyStruct(&citydb, result)

	return nil
}

type GeoIPMockASN struct {
}

func (g *GeoIPMockASN) Lookup(ipAddress net.IP, result interface{}) error {
	var asnDb ASNDb
	asnDb.Autonomous_system_number = 42
	asnDb.Autonomous_system_organization = "forty two"

	CopyStruct(&asnDb, result)

	return nil
}

func CopyStruct(src interface{}, dst interface{}) {
	s := reflect.Indirect(reflect.ValueOf(src))
	d := reflect.Indirect(reflect.ValueOf(dst))
	CopyStructRec(s, d)
}

func CopyStructRec(s, d reflect.Value) {
	st := s.Type()
	dt := d.Type()

	typeOft1 := s.Type()
	typeOft2 := d.Type()

	for i := 0; i < s.NumField(); i++ {
		sf := s.Field(i)

		if st.Field(i).Type.Kind() == reflect.Struct {
			for j := 0; j < d.NumField(); j++ {
				if typeOft1.Field(i).Name == typeOft2.Field(j).Name {
					CopyStructRec(s.Field(i), d.Field(j))
					goto cont
				}
			}
		}

		for j := 0; j < d.NumField(); j++ {
			df := d.Field(j)
			dtf := dt.Field(j)
			dsttag := dtf.Tag.Get("maxminddb")

			if strings.ToLower(typeOft1.Field(i).Name) == strings.ToLower(dsttag) {
				df.Set(reflect.Value(sf))
				break
			}
		}
	cont:
	}
}
