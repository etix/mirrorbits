// Copyright (c) 2014-2019 Ludovic Fauvet
// Licensed under the MIT license

package mirrors

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"

	. "github.com/etix/mirrorbits/config"
	"github.com/etix/mirrorbits/core"
	"github.com/etix/mirrorbits/database"
	"github.com/etix/mirrorbits/filesystem"
	"github.com/etix/mirrorbits/network"
	"github.com/etix/mirrorbits/utils"
	"github.com/gomodule/redigo/redis"
)

type Protocol uint

const (
	UNDEFINED Protocol = iota
	HTTP
	HTTPS
)

func (p Protocol) String() string {
	switch p {
	case UNDEFINED:
		return "undefined"
	case HTTP:
		return "HTTP"
	case HTTPS:
		return "HTTPS"
	default:
		return "unknown"
	}
}

// Mirror is the structure representing all the information about a mirror
type Mirror struct {
	ID                          int              `redis:"ID" yaml:"-"`
	Name                        string           `redis:"name" yaml:"Name"`
	HttpURL                     string           `redis:"http" yaml:"HttpURL"`
	RsyncURL                    string           `redis:"rsync" yaml:"RsyncURL"`
	FtpURL                      string           `redis:"ftp" yaml:"FtpURL"`
	SponsorName                 string           `redis:"sponsorName" yaml:"SponsorName"`
	SponsorURL                  string           `redis:"sponsorURL" yaml:"SponsorURL"`
	SponsorLogoURL              string           `redis:"sponsorLogo" yaml:"SponsorLogoURL"`
	AdminName                   string           `redis:"adminName" yaml:"AdminName"`
	AdminEmail                  string           `redis:"adminEmail" yaml:"AdminEmail"`
	CustomData                  string           `redis:"customData" yaml:"CustomData"`
	ContinentOnly               bool             `redis:"continentOnly" yaml:"ContinentOnly"`
	CountryOnly                 bool             `redis:"countryOnly" yaml:"CountryOnly"`
	ASOnly                      bool             `redis:"asOnly" yaml:"ASOnly"`
	Score                       int              `redis:"score" yaml:"Score"`
	Latitude                    float32          `redis:"latitude" yaml:"Latitude"`
	Longitude                   float32          `redis:"longitude" yaml:"Longitude"`
	ContinentCode               string           `redis:"continentCode" yaml:"ContinentCode"`
	CountryCodes                string           `redis:"countryCodes" yaml:"CountryCodes"`
	ExcludedCountryCodes        string           `redis:"excludedCountryCodes" yaml:"ExcludedCountryCodes"`
	Asnum                       uint             `redis:"asnum" yaml:"ASNum"`
	Comment                     string           `redis:"comment" yaml:"-"`
	Enabled                     bool             `redis:"enabled" yaml:"Enabled"`
	Up                          bool             `redis:"up" json:"-" yaml:"-"`
	DownReason                  string           `redis:"downReason" json:",omitempty" yaml:"-"`
	StateSince                  Time             `redis:"stateSince" json:",omitempty" yaml:"-"`
	AllowRedirects              Redirects        `redis:"allowredirects" json:",omitempty" yaml:"AllowRedirects"`
	TZOffset                    int64            `redis:"tzoffset" json:"-" yaml:"-"` // timezone offset in ms
	Distance                    float32          `redis:"-" yaml:"-"`
	CountryFields               []string         `redis:"-" json:"-" yaml:"-"`
	ExcludedCountryFields       []string         `redis:"-" json:"-" yaml:"-"`
	Filepath                    string           `redis:"-" json:"-" yaml:"-"`
	Weight                      float32          `redis:"-" json:"-" yaml:"-"`
	ComputedScore               int              `redis:"-" yaml:"-"`
	LastSync                    Time             `redis:"lastSync" yaml:"-"`
	LastSuccessfulSync          Time             `redis:"lastSuccessfulSync" yaml:"-"`
	LastSuccessfulSyncProtocol  core.ScannerType `redis:"lastSuccessfulSyncProtocol" yaml:"-"`
	LastSuccessfulSyncPrecision core.Precision   `redis:"lastSuccessfulSyncPrecision" yaml:"-"`
	LastModTime                 Time             `redis:"lastModTime" yaml:"-"`

	FileInfo *filesystem.FileInfo `redis:"-" json:"-" yaml:"-"` // Details of the requested file on this specific mirror
	AbsoluteURL string            `redis:"-" yaml:"-"` // Absolute HttpURL, guaranteed to start with a scheme
	ExcludeReason string          `redis:"-" json:",omitempty" yaml:"-"` // Reason why the mirror was excluded
}

// Prepare must be called after retrieval from the database to reformat some values
func (m *Mirror) Prepare() {
	m.CountryFields = strings.Fields(m.CountryCodes)
	m.ExcludedCountryFields = strings.Fields(m.ExcludedCountryCodes)
}

// IsHTTPS returns true if the mirror has an HTTPS address
func (m *Mirror) IsHTTPS() bool {
	return strings.HasPrefix(m.HttpURL, "https://")
}

// IsUp returns true if the mirror is up
func (m *Mirror) IsUp() bool {
	return m.Up
}

// Mirrors represents a slice of Mirror
type Mirrors []Mirror

// Len return the number of Mirror in the slice
func (s Mirrors) Len() int { return len(s) }

// Swap swaps mirrors at index i and j
func (s Mirrors) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

// ByRank is used to sort a slice of Mirror by their rank
type ByRank struct {
	Mirrors
	ClientInfo network.GeoIPRecord
}

// Less compares two mirrors based on their rank
func (m ByRank) Less(i, j int) bool {
	if m.ClientInfo.IsValid() {
		if m.ClientInfo.ASNum == m.Mirrors[i].Asnum {
			if m.Mirrors[i].Asnum != m.Mirrors[j].Asnum {
				return true
			}
		} else if m.ClientInfo.ASNum == m.Mirrors[j].Asnum {
			return false
		}

		//TODO Simplify me
		if m.ClientInfo.CountryCode != "" {
			if utils.IsInSlice(m.ClientInfo.CountryCode, m.Mirrors[i].CountryFields) {
				if !utils.IsInSlice(m.ClientInfo.CountryCode, m.Mirrors[j].CountryFields) {
					return true
				}
			} else if utils.IsInSlice(m.ClientInfo.CountryCode, m.Mirrors[j].CountryFields) {
				return false
			}
		}
		if m.ClientInfo.ContinentCode != "" {
			if m.ClientInfo.ContinentCode == m.Mirrors[i].ContinentCode {
				if m.ClientInfo.ContinentCode != m.Mirrors[j].ContinentCode {
					return true
				}
			} else if m.ClientInfo.ContinentCode == m.Mirrors[j].ContinentCode {
				return false
			}
		}

		return m.Mirrors[i].Distance < m.Mirrors[j].Distance
	}
	// Randomize the output if we miss client info
	return rand.Intn(2) == 0
}

// ByComputedScore is used to sort a slice of Mirror by their score
type ByComputedScore struct {
	Mirrors
}

// Less compares two mirrors based on their score
func (b ByComputedScore) Less(i, j int) bool {
	return b.Mirrors[i].ComputedScore > b.Mirrors[j].ComputedScore
}

// ByExcludeReason is used to sort a slice of Mirror alphabetically by their exclude reason
type ByExcludeReason struct {
	Mirrors
}

// Less compares two mirrors based on their exclude reason
func (b ByExcludeReason) Less(i, j int) bool {
	if b.Mirrors[i].ExcludeReason < b.Mirrors[j].ExcludeReason {
		return true
	}
	return false
}

// EnableMirror enables the given mirror
func EnableMirror(r *database.Redis, id int) error {
	return SetMirrorEnabled(r, id, true)
}

// DisableMirror disables the given mirror
func DisableMirror(r *database.Redis, id int) error {
	return SetMirrorEnabled(r, id, false)
}

// SetMirrorEnabled marks a mirror as enabled or disabled
func SetMirrorEnabled(r *database.Redis, id int, state bool) error {
	conn := r.Get()
	defer conn.Close()

	key := fmt.Sprintf("MIRROR_%d", id)
	_, err := conn.Do("HMSET", key, "enabled", state)

	// Publish update
	if err == nil {
		database.Publish(conn, database.MIRROR_UPDATE, strconv.Itoa(id))

		if state == true {
			PushLog(r, NewLogEnabled(id))
		} else {
			PushLog(r, NewLogDisabled(id))
		}
	}

	return err
}

// MarkMirrorUp marks the given mirror as up
func MarkMirrorUp(r *database.Redis, id int, proto Protocol) error {
	return SetMirrorState(r, id, proto, true, "")
}

// MarkMirrorDown marks the given mirror as down
func MarkMirrorDown(r *database.Redis, id int, proto Protocol, reason string) error {
	return SetMirrorState(r, id, proto, false, reason)
}

// SetMirrorState sets the state of a mirror to up or down with an optional reason
func SetMirrorState(r *database.Redis, id int, proto Protocol, state bool, reason string) error {
	conn := r.Get()
	defer conn.Close()

	key := fmt.Sprintf("MIRROR_%d", id)

	previousState, err := redis.Bool(conn.Do("HGET", key, "up"))
	if err != nil && err != redis.ErrNil {
		return err
	}

	var args []interface{}
	args = append(args, key, "up", state, "downReason", reason)

	if state != previousState {
		args = append(args, "stateSince", time.Now().Unix())
	}

	_, err = conn.Do("HMSET", args...)

	if err == nil {
		// Publish update
		database.Publish(conn, database.MIRROR_UPDATE, strconv.Itoa(id))

		if state != previousState {
			PushLog(r, NewLogStateChanged(id, proto, state, reason))
		}
	}

	return err
}

// Results is the resulting struct of a request and is
// used by the renderers to generate the final page.
type Results struct {
	FileInfo     filesystem.FileInfo
	IP           string
	ClientInfo   network.GeoIPRecord
	MirrorList   Mirrors
	ExcludedList Mirrors `json:",omitempty"`
	Fallback     bool    `json:",omitempty"`
	LocalJSPath  string
}

// Redirects is handling the per-mirror authorization of HTTP redirects
type Redirects int

// Allowed will return true if redirects are authorized for this mirror
func (r *Redirects) Allowed() bool {
	switch *r {
	case 1:
		return true
	case 2:
		return false
	default:
		return GetConfig().DisallowRedirects == false
	}
}

// MarshalYAML converts internal values to YAML
func (r Redirects) MarshalYAML() (interface{}, error) {
	var b *bool
	switch r {
	case 1:
		v := true
		b = &v
	case 2:
		v := false
		b = &v
	default:
	}
	return b, nil
}

// UnmarshalYAML converts YAML to internal values
func (r *Redirects) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var b *bool
	if err := unmarshal(&b); err != nil {
		return err
	}
	if b == nil {
		*r = 0
	} else if *b == true {
		*r = 1
	} else {
		*r = 2
	}
	return nil
}

// Time is a structure holding a time.Time object.
// It is used to serialize and deserialize a time
// held in a redis database.
type Time struct {
	time.Time
}

// RedisArg serialize the time.Time object
func (t Time) RedisArg() interface{} {
	return t.UTC().Unix()
}

// RedisScan deserialize the time.Time object
func (t *Time) RedisScan(src interface{}) (err error) {
	switch src := src.(type) {
	case int64:
		t.Time = time.Unix(src, 0)
	case []byte:
		var i int64
		i, err = strconv.ParseInt(string(src), 10, 64)
		t.Time = time.Unix(i, 0)
	default:
		err = fmt.Errorf("cannot convert from %T to %T", src, t)
	}
	return err
}

// FromTime returns a Time from a time.Time
func (t Time) FromTime(time time.Time) Time {
	return Time{
		Time: time,
	}
}
