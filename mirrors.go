// Copyright (c) 2014 Ludovic Fauvet
// Licensed under the MIT license

package main

import (
	"math/rand"
)

type Mirror struct {
	ID             string   `redis:"ID" yaml:"-"`
	HttpURL        string   `redis:"http" yaml:"HttpURL"`
	RsyncURL       string   `redis:"rsync" yaml:"RsyncURL"`
	FtpURL         string   `redis:"ftp" yaml:"FtpURL"`
	SponsorName    string   `redis:"sponsorName" yaml:"SponsorName"`
	SponsorURL     string   `redis:"sponsorURL" yaml:"SponsorURL"`
	SponsorLogoURL string   `redis:"sponsorLogo" yaml:"SponsorLogoURL"`
	AdminName      string   `redis:"adminName" yaml:"AdminName"`
	AdminEmail     string   `redis:"adminEmail" yaml:"AdminEmail"`
	CustomData     string   `redis:"customData" yaml:"CustomData"`
	ContinentOnly  bool     `redis:"continentOnly" yaml:"ContinentOnly"`
	CountryOnly    bool     `redis:"countryOnly" yaml:"CountryOnly"`
	ASOnly         bool     `redis:"asOnly" yaml:"ASOnly"`
	Score          int      `redis:"score" yaml:"Score"`
	Latitude       float32  `redis:"latitude" yaml:"Latitude"`
	Longitude      float32  `redis:"longitude" yaml:"Longitude"`
	ContinentCode  string   `redis:"continentCode" yaml:"ContinentCode"`
	CountryCodes   string   `redis:"countryCodes" yaml:"CountryCodes"`
	Asnum          int      `redis:"asnum" yaml:"ASNum"`
	Enabled        bool     `redis:"enabled" yaml:"Enabled"`
	Up             bool     `redis:"up" json:"-" yaml:"-"`
	ExcludeReason  string   `redis:"excludeReason" json:",omitempty" yaml:"-"`
	StateSince     int64    `redis:"stateSince" json:",omitempty" yaml:"-"`
	Distance       float32  `redis:"-" yaml:"-"`
	CountryFields  []string `redis:"-" json:"-" yaml:"-"`
	Weight         int      `redis:"-" json:"-" yaml:"-"`
	LastSync       int64    `redis:"lastSync" yaml:"-"`

	FileInfo *FileInfo `redis:"-" json:"-" yaml:"-"` // Details of the requested file on this specific mirror
}

type Mirrors []Mirror

func (s Mirrors) Len() int      { return len(s) }
func (s Mirrors) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

type ByRank struct {
	Mirrors
	clientInfo GeoIPRec
}

func (m ByRank) Less(i, j int) bool {
	if m.clientInfo.isValid() {
		if m.clientInfo.ASNum == m.Mirrors[i].Asnum {
			if m.Mirrors[i].Asnum != m.Mirrors[j].Asnum {
				return true
			}
		} else if m.clientInfo.ASNum == m.Mirrors[j].Asnum {
			return false
		}

		//TODO Simplify me
		if m.clientInfo.CountryCode != "" {
			if isInSlice(m.clientInfo.CountryCode, m.Mirrors[i].CountryFields) {
				if !isInSlice(m.clientInfo.CountryCode, m.Mirrors[j].CountryFields) {
					return true
				}
			} else if isInSlice(m.clientInfo.CountryCode, m.Mirrors[j].CountryFields) {
				return false
			}
		} else if m.clientInfo.ContinentCode != "" {
			if m.clientInfo.ContinentCode == m.Mirrors[i].ContinentCode {
				if m.clientInfo.ContinentCode != m.Mirrors[j].ContinentCode {
					return true
				}
			} else if m.clientInfo.ContinentCode == m.Mirrors[j].ContinentCode {
				return false
			}
		}

		return m.Mirrors[i].Distance < m.Mirrors[j].Distance
	} else {
		// Randomize the output if we miss client info
		//TODO randomize accross "primary" mirrors only (4-5 biggest)
		return rand.Intn(2) == 0
	}
}

type ByWeight struct {
	Mirrors
	weights map[string]int
}

func (b ByWeight) Less(i, j int) bool {
	w1, ok1 := b.weights[b.Mirrors[i].ID]
	w2, ok2 := b.weights[b.Mirrors[j].ID]
	if ok1 && ok2 {
		return w1 > w2
	} else if ok1 && !ok2 {
		return true
	} else if !ok1 && ok2 {
		return false
	}
	return false
}

type ByExcludeReason struct {
	Mirrors
}

func (b ByExcludeReason) Less(i, j int) bool {
	if b.Mirrors[i].ExcludeReason < b.Mirrors[j].ExcludeReason {
		return true
	}
	return false
}
