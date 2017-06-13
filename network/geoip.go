// Copyright (c) 2014-2017 Ludovic Fauvet
// Licensed under the MIT license

package network

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/etix/geoip"
	. "github.com/etix/mirrorbits/config"
	"github.com/op/go-logging"
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
	sync.RWMutex

	geo  *geoipDB
	geo6 *geoipDB
	asn  *geoipDB
	asn6 *geoipDB
}

// GeoIPRec defines a GeoIP record for a given IP address
type GeoIPRecord struct {
	*geoip.GeoIPRecord
	ASName    string
	ASNum     int
	ASNetmask int
}

// Geolocalizer is an interface representing a GeoIP library
type Geolocalizer interface {
	GetRecord(ip string) *geoip.GeoIPRecord
	GetName(ip string) (name string, netmask int)
}

// NewGeoIP instanciates a new instance of GeoIP
func NewGeoIP() *GeoIP {
	return &GeoIP{}
}

// Open the GeoIP database
func (g *GeoIP) openDatabase(file string) (*geoip.GeoIP, time.Time, error) {
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
	}

	if fi != nil {
		modTime = fi.ModTime()
	}

	db, err := geoip.Open(filename)
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

type GeoIPError struct {
	Errors []error
	loaded int
}

func (e GeoIPError) Error() string {
	return "One or more GeoIP database could not be loaded"
}

func (e GeoIPError) IsFatal() bool {
	return e.loaded == len(e.Errors)
}

// Try to load the GeoIP databases into memory
func (g *GeoIP) LoadGeoIP() error {
	var ret GeoIPError

	g.Lock()
	g.loadDB("GeoLiteCity.dat", &g.geo, &ret)
	g.loadDB("GeoLiteCityv6.dat", &g.geo6, &ret)
	g.loadDB("GeoIPASNum.dat", &g.asn, &ret)
	g.loadDB("GeoIPASNumv6.dat", &g.asn6, &ret)
	g.Unlock()

	if len(ret.Errors) > 0 {
		return ret
	}
	return nil
}

// Get details about a given ip address (might be v4 or v6)
func (g *GeoIP) GetRecord(ip string) (ret GeoIPRecord) {
	g.RLock()
	if g.IsIPv6(ip) {
		if g.geo6 != nil && g.geo6.db != nil {
			ret.GeoIPRecord = g.geo6.db.GetRecord(ip)
		}
		if g.asn6 != nil && g.asn6.db != nil {
			ret.ASName, ret.ASNetmask = g.asn6.db.GetName(ip)
		}
	} else {
		if g.geo != nil && g.geo.db != nil {
			ret.GeoIPRecord = g.geo.db.GetRecord(ip)
		}
		if g.asn != nil && g.asn.db != nil {
			ret.ASName, ret.ASNetmask = g.asn.db.GetName(ip)
		}
	}
	g.RUnlock()
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
