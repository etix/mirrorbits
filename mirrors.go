// Copyright (c) 2014 Ludovic Fauvet
// Licensed under the MIT license

package main

import (
	"math/rand"
)

// Mirror is the structure representing all the informations about a mirror
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
	Comment        string   `redis:"comment" yaml:"-"`
	Enabled        bool     `redis:"enabled" yaml:"Enabled"`
	Up             bool     `redis:"up" json:"-" yaml:"-"`
	ExcludeReason  string   `redis:"excludeReason" json:",omitempty" yaml:"-"`
	StateSince     int64    `redis:"stateSince" json:",omitempty" yaml:"-"`
	Distance       float32  `redis:"-" yaml:"-"`
	CountryFields  []string `redis:"-" json:"-" yaml:"-"`
	Weight         int      `redis:"-" json:"-" yaml:"-"`
	LastSync       int64    `redis:"lastSync" yaml:"-"`
	ComputedScore  int      `redis:"lastSync" yaml:"-"`

	FileInfo *FileInfo `redis:"-" json:"-" yaml:"-"` // Details of the requested file on this specific mirror
}

// Mirrors represents a slice of Mirror
type Mirrors []Mirror

func (s Mirrors) Len() int      { return len(s) }
func (s Mirrors) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

// ByRank is used to sort a slice of Mirror by their rank
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
