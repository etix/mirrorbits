// Copyright (c) 2014-2015 Ludovic Fauvet
// Licensed under the MIT license

package mirrors

import (
	"bytes"
	"fmt"
	"github.com/etix/mirrorbits/database"
	"github.com/etix/mirrorbits/filesystem"
	"github.com/etix/mirrorbits/network"
	"github.com/etix/mirrorbits/utils"
	"github.com/garyburd/redigo/redis"
	"math/rand"
	"time"
)

// Mirror is the structure representing all the information about a mirror
type Mirror struct {
	ID                 string   `redis:"ID" yaml:"-"`
	HttpURL            string   `redis:"http" yaml:"HttpURL"`
	RsyncURL           string   `redis:"rsync" yaml:"RsyncURL"`
	FtpURL             string   `redis:"ftp" yaml:"FtpURL"`
	SponsorName        string   `redis:"sponsorName" yaml:"SponsorName"`
	SponsorURL         string   `redis:"sponsorURL" yaml:"SponsorURL"`
	SponsorLogoURL     string   `redis:"sponsorLogo" yaml:"SponsorLogoURL"`
	AdminName          string   `redis:"adminName" yaml:"AdminName"`
	AdminEmail         string   `redis:"adminEmail" yaml:"AdminEmail"`
	CustomData         string   `redis:"customData" yaml:"CustomData"`
	ContinentOnly      bool     `redis:"continentOnly" yaml:"ContinentOnly"`
	CountryOnly        bool     `redis:"countryOnly" yaml:"CountryOnly"`
	ASOnly             bool     `redis:"asOnly" yaml:"ASOnly"`
	Score              int      `redis:"score" yaml:"Score"`
	Latitude           float32  `redis:"latitude" yaml:"Latitude"`
	Longitude          float32  `redis:"longitude" yaml:"Longitude"`
	ContinentCode      string   `redis:"continentCode" yaml:"ContinentCode"`
	CountryCodes       string   `redis:"countryCodes" yaml:"CountryCodes"`
	Asnum              int      `redis:"asnum" yaml:"ASNum"`
	Comment            string   `redis:"comment" yaml:"-"`
	Enabled            bool     `redis:"enabled" yaml:"Enabled"`
	Up                 bool     `redis:"up" json:"-" yaml:"-"`
	ExcludeReason      string   `redis:"excludeReason" json:",omitempty" yaml:"-"`
	StateSince         int64    `redis:"stateSince" json:",omitempty" yaml:"-"`
	Distance           float32  `redis:"-" yaml:"-"`
	CountryFields      []string `redis:"-" json:"-" yaml:"-"`
	Filepath           string   `redis:"-" json:"-" yaml:"-"`
	Weight             float32  `redis:"-" json:"-" yaml:"-"`
	ComputedScore      int      `redis:"-" yaml:"-"`
	LastSync           int64    `redis:"lastSync" yaml:"-"`
	LastSuccessfulSync int64    `redis:"lastSuccessfulSync" yaml:"-"`

	FileInfo *filesystem.FileInfo `redis:"-" json:"-" yaml:"-"` // Details of the requested file on this specific mirror
}

// Mirrors represents a slice of Mirror
type Mirrors []Mirror

func (s Mirrors) Len() int      { return len(s) }
func (s Mirrors) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

// ByRank is used to sort a slice of Mirror by their rank
type ByRank struct {
	Mirrors
	ClientInfo network.GeoIPRec
}

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
		} else if m.ClientInfo.ContinentCode != "" {
			if m.ClientInfo.ContinentCode == m.Mirrors[i].ContinentCode {
				if m.ClientInfo.ContinentCode != m.Mirrors[j].ContinentCode {
					return true
				}
			} else if m.ClientInfo.ContinentCode == m.Mirrors[j].ContinentCode {
				return false
			}
		}

		return m.Mirrors[i].Distance < m.Mirrors[j].Distance
	} else {
		// Randomize the output if we miss client info
		return rand.Intn(2) == 0
	}
}

// ByComputedScore is used to sort a slice of Mirror by their rank
type ByComputedScore struct {
	Mirrors
}

func (b ByComputedScore) Less(i, j int) bool {
	return b.Mirrors[i].ComputedScore > b.Mirrors[j].ComputedScore
}

// ByExcludeReason is used to sort a slice of Mirror alphabetically by their exclude reason
type ByExcludeReason struct {
	Mirrors
}

func (b ByExcludeReason) Less(i, j int) bool {
	if b.Mirrors[i].ExcludeReason < b.Mirrors[j].ExcludeReason {
		return true
	}
	return false
}

func EnableMirror(r *database.Redis, id string) error {
	return SetMirrorEnabled(r, id, true)
}

func DisableMirror(r *database.Redis, id string) error {
	return SetMirrorEnabled(r, id, false)
}

func SetMirrorEnabled(r *database.Redis, id string, state bool) error {
	conn := r.Get()
	defer conn.Close()

	key := fmt.Sprintf("MIRROR_%s", id)
	_, err := conn.Do("HMSET", key, "enabled", state)

	// Publish update
	database.Publish(conn, database.MIRROR_UPDATE, id)

	return err
}

func MarkMirrorUp(r *database.Redis, id string) error {
	return SetMirrorState(r, id, true, "")
}

func MarkMirrorDown(r *database.Redis, id string, reason string) error {
	return SetMirrorState(r, id, false, reason)
}

func SetMirrorState(r *database.Redis, id string, state bool, reason string) error {
	conn := r.Get()
	defer conn.Close()

	key := fmt.Sprintf("MIRROR_%s", id)

	previousState, err := redis.Bool(conn.Do("HGET", key, "up"))
	if err != nil {
		return err
	}

	var args []interface{}
	args = append(args, key, "up", state, "excludeReason", reason)

	if state != previousState {
		args = append(args, "stateSince", time.Now().Unix())
	}

	_, err = conn.Do("HMSET", args...)

	if state != previousState {
		// Publish update
		database.Publish(conn, database.MIRROR_UPDATE, id)
	}

	return err
}

func GetMirrorMapUrl(mirrors Mirrors, clientInfo network.GeoIPRec) string {
	var buffer bytes.Buffer
	buffer.WriteString("//maps.googleapis.com/maps/api/staticmap?size=520x320&sensor=false&visual_refresh=true")

	if clientInfo.IsValid() {
		buffer.WriteString(fmt.Sprintf("&markers=size:mid|color:red|%f,%f", clientInfo.Latitude, clientInfo.Longitude))
	}

	count := 1
	for i, mirror := range mirrors {
		if count > 9 {
			break
		}
		if i == 0 && clientInfo.IsValid() {
			// Draw a path between the client and the mirror
			buffer.WriteString(fmt.Sprintf("&path=color:0x17ea0bdd|weight:5|%f,%f|%f,%f",
				clientInfo.Latitude, clientInfo.Longitude,
				mirror.Latitude, mirror.Longitude))
		}
		color := "blue"
		if mirror.Weight > 0 {
			color = "green"
		}
		buffer.WriteString(fmt.Sprintf("&markers=color:%s|label:%d|%f,%f", color, count, mirror.Latitude, mirror.Longitude))
		count++
	}
	return buffer.String()
}

// Results is the resulting struct of a request and is
// used by the renderers to generate the final page.
type Results struct {
	FileInfo     filesystem.FileInfo
	MapURL       string `json:"-"`
	IP           string
	ClientInfo   network.GeoIPRec
	MirrorList   Mirrors
	ExcludedList Mirrors `json:",omitempty"`
	Fallback     bool    `json:",omitempty"`
}
