// Copyright (c) 2014-2017 Ludovic Fauvet
// Licensed under the MIT license

package network

import (
	"errors"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	. "github.com/etix/mirrorbits/config"
	"github.com/op/go-logging"
	"github.com/oschwald/maxminddb-golang"
)

var (
	// ErrMultipleAddresses is returned when the mirror has more than one address
	ErrMultipleAddresses = errors.New("the mirror has more than one IP address")
	log                  = logging.MustGetLogger("main")
)

const (
	geoipUpdatedExt = ".updated"
)

// GeoIP contains methods to query the GeoIP database
type GeoIP struct {
	sync.RWMutex

	city *geoipDB
	asn  *geoipDB
}

// GeoIPRecord defines a GeoIP record for a given IP address
type GeoIPRecord struct {
	// City DB
	CountryCode   string
	ContinentCode string
	City          string
	Country       string
	Latitude      float32
	Longitude     float32

	// ASN DB
	ASName string
	ASNum  uint
}

// Geolocalizer is an interface representing a GeoIP library
type Geolocalizer interface {
	Lookup(ipAddress net.IP, result interface{}) error
}

// NewGeoIP instanciates a new instance of GeoIP
func NewGeoIP() *GeoIP {
	return &GeoIP{}
}

// Open the GeoIP database
func (g *GeoIP) openDatabase(file string) (*maxminddb.Reader, time.Time, error) {
	dbpath := GetConfig().GeoipDatabasePath
	if dbpath != "" && !strings.HasSuffix(dbpath, "/") {
		dbpath += "/"
	}

	filename := dbpath + file

	var err error
	var fi os.FileInfo
	var modTime time.Time

	if fi, err = os.Stat(filename + geoipUpdatedExt); !os.IsNotExist(err) {
		filename += geoipUpdatedExt
	} else {
		fi, err = os.Stat(filename)
		if err != nil {
			return nil, time.Time{}, err
		}
	}

	if fi != nil {
		modTime = fi.ModTime()
	}

	db, err := maxminddb.Open(filename)
	return db, modTime, err
}

type geoipDB struct {
	filename string
	modTime  time.Time
	db       Geolocalizer
}

func (g *GeoIP) loadDB(filename string, geodb **geoipDB, geoiperror *GeoIPError) error {
	// Increase the loaded counter
	geoiperror.loaded++

	if *geodb == nil {
		*geodb = &geoipDB{
			filename: filename,
		}
	}

	db, modTime, err := g.openDatabase(filename)
	if err != nil {
		geoiperror.Errors = append(geoiperror.Errors, err)
		return err
	}
	if (*geodb).modTime.Equal(modTime) {
		return nil
	}

	(*geodb).db = db
	(*geodb).modTime = modTime

	log.Infof("Loading %s database (updated on %s)", filename, (*geodb).modTime)
	return nil
}

// GeoIPError holds errors while loading the different databases
type GeoIPError struct {
	Errors []error
	loaded int
}

func (e GeoIPError) Error() string {
	return "One or more GeoIP database could not be loaded"
}

// IsFatal returns true if the error is fatal
func (e GeoIPError) IsFatal() bool {
	return e.loaded == len(e.Errors)
}

// LoadGeoIP loads the GeoIP databases into memory
func (g *GeoIP) LoadGeoIP() error {
	var ret GeoIPError

	g.Lock()
	g.loadDB("GeoLite2-City.mmdb", &g.city, &ret)
	g.loadDB("GeoLite2-ASN.mmdb", &g.asn, &ret)
	g.Unlock()

	if len(ret.Errors) > 0 {
		return ret
	}
	return nil
}

// GetRecord return informations about the given ip address
// (works in IPv4 and v6)
func (g *GeoIP) GetRecord(ip string) (ret GeoIPRecord) {
	addr := net.ParseIP(ip)
	if addr == nil {
		return GeoIPRecord{}
	}

	type CityDb struct {
		City struct {
			Names struct {
				English string `maxminddb:"en"`
			} `maxminddb:"names"`
		} `maxminddb:"city"`
		Country struct {
			IsoCode string `maxminddb:"iso_code"`
			Names   struct {
				English string `maxminddb:"en"`
			} `maxminddb:"names"`
		} `maxminddb:"country"`
		Continent struct {
			Code string `maxminddb:"code"`
		} `maxminddb:"continent"`
		Location struct {
			Latitude  float64 `maxminddb:"latitude"`
			Longitude float64 `maxminddb:"longitude"`
		} `maxminddb:"location"`
	}

	type ASNDb struct {
		AutonomousSystemNumber uint   `maxminddb:"autonomous_system_number"`
		AutonomousSystemOrg    string `maxminddb:"autonomous_system_organization"`
	}

	var err error
	var cityDb CityDb
	var asnDb ASNDb

	g.RLock()
	defer g.RUnlock()

	if g.city != nil && g.city.db != nil {
		err = g.city.db.Lookup(addr, &cityDb)
		if err != nil {
			return GeoIPRecord{}
		}
		ret.CountryCode = cityDb.Country.IsoCode
		ret.ContinentCode = cityDb.Continent.Code
		ret.City = cityDb.City.Names.English
		ret.Country = cityDb.Country.Names.English
		ret.Latitude = float32(cityDb.Location.Latitude)
		ret.Longitude = float32(cityDb.Location.Longitude)
	}
	if g.asn != nil && g.asn.db != nil {
		err = g.asn.db.Lookup(addr, &asnDb)
		if err != nil {
			return GeoIPRecord{}
		}
		ret.ASName = asnDb.AutonomousSystemOrg
		ret.ASNum = asnDb.AutonomousSystemNumber
	}

	return ret
}

// IsIPv6 returns true if the given address is of version 6
func (g *GeoIP) IsIPv6(ip string) bool {
	return strings.Contains(ip, ":")
}

// IsValid returns true if the given address is valid
func (g *GeoIPRecord) IsValid() bool {
	return len(g.CountryCode) > 0
}
