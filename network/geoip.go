// Copyright (c) 2014-2015 Ludovic Fauvet
// Licensed under the MIT license

package network

import (
	"errors"
	"github.com/etix/geoip"
	. "github.com/etix/mirrorbits/config"
	"github.com/op/go-logging"
	"os"
	"strconv"
	"strings"
)

var (
	ErrMultipleAddresses = errors.New("The mirror has more than one IP address")
	log                  = logging.MustGetLogger("main")
)

const (
	geoipUpdatedExt = ".updated"
)

// GeoIP contains methods to query the GeoIP database
type GeoIP struct {
	geo  *geoip.GeoIP
	geo6 *geoip.GeoIP
	asn  *geoip.GeoIP
	asn6 *geoip.GeoIP
}

// GeoIPRec defines a GeoIP record for a given IP address
type GeoIPRecord struct {
	*geoip.GeoIPRecord
	ASName    string
	ASNum     int
	ASNetmask int
}

// NewGeoIP instanciates a new instance of GeoIP
func NewGeoIP() *GeoIP {
	return &GeoIP{}
}

// Open the GeoIP database
func (g *GeoIP) openDatabase(file string) (*geoip.GeoIP, error) {
	dbpath := GetConfig().GeoipDatabasePath
	if len(dbpath) > 0 {
		dbpath += "/"
	}

	filename := dbpath + file

	if _, err := os.Stat(filename + geoipUpdatedExt); !os.IsNotExist(err) {
		filename += geoipUpdatedExt
	}

	return geoip.Open(filename)
}

// Try to load the GeoIP databases into memory
func (g *GeoIP) LoadGeoIP() (err error) {
	g.geo, err = g.openDatabase("GeoLiteCity.dat")
	if err != nil {
		log.Critical("Could not open GeoLiteCity database: %s\n", err.Error())
	}

	g.geo6, err = g.openDatabase("GeoLiteCityv6.dat")
	if err != nil {
		log.Critical("Could not open GeoLiteCityv6.dat database: %s\n", err.Error())
	}

	g.asn, err = g.openDatabase("GeoIPASNum.dat")
	if err != nil {
		log.Critical("Could not open GeoIPASNum database: %s\n", err.Error())
	}

	g.asn6, err = g.openDatabase("GeoIPASNumv6.dat")
	if err != nil {
		log.Critical("Could not open GeoIPASNumv6 database: %s\n", err.Error())
	}
	return err
}

// Get details about a given ip address (might be v4 or v6)
func (g *GeoIP) GetRecord(ip string) (ret GeoIPRecord) {
	if g.IsIPv6(ip) {
		if g.geo6 != nil {
			ret.GeoIPRecord = g.geo6.GetRecord(ip)
		}
		if g.asn6 != nil {
			ret.ASName, ret.ASNetmask = g.asn6.GetName(ip)
		}
	} else {
		if g.geo != nil {
			ret.GeoIPRecord = g.geo.GetRecord(ip)
		}
		if g.asn != nil {
			ret.ASName, ret.ASNetmask = g.asn.GetName(ip)
		}
	}
	if len(ret.ASName) > 0 {
		// Split the ASNum (i.e "AS12322 Free SAS")
		ss := strings.SplitN(ret.ASName, " ", 2)
		if len(ss) == 2 {
			ret.ASNum, _ = strconv.Atoi(strings.TrimPrefix(ss[0], "AS"))
			ret.ASName = ss[1]
		}
	}
	return ret
}

// Return true if the given address is of version 6
func (g *GeoIP) IsIPv6(ip string) bool {
	return strings.Contains(ip, ":")
}

// Return true if the given address is valid
func (g *GeoIPRecord) IsValid() bool {
	return g.GeoIPRecord != nil
}
