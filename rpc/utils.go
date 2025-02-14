// Copyright (c) 2014-2019 Ludovic Fauvet
// Licensed under the MIT license

package rpc

import (
	"github.com/etix/mirrorbits/mirrors"
	"github.com/golang/protobuf/ptypes"
)

func MirrorToRPC(m *mirrors.Mirror) (*Mirror, error) {
	stateSince, err := ptypes.TimestampProto(m.StateSince.Time)
	if err != nil {
		return nil, err
	}
	lastSync, err := ptypes.TimestampProto(m.LastSync.Time)
	if err != nil {
		return nil, err
	}
	lastSuccessfulSync, err := ptypes.TimestampProto(m.LastSuccessfulSync.Time)
	if err != nil {
		return nil, err
	}
	lastModTime, err := ptypes.TimestampProto(m.LastModTime.Time)
	if err != nil {
		return nil, err
	}
	return &Mirror{
		ID:                   int32(m.ID),
		Name:                 m.Name,
		HttpURL:              m.HttpURL,
		RsyncURL:             m.RsyncURL,
		FtpURL:               m.FtpURL,
		SponsorName:          m.SponsorName,
		SponsorURL:           m.SponsorURL,
		SponsorLogoURL:       m.SponsorLogoURL,
		AdminName:            m.AdminName,
		AdminEmail:           m.AdminEmail,
		CustomData:           m.CustomData,
		ContinentOnly:        m.ContinentOnly,
		CountryOnly:          m.CountryOnly,
		ASOnly:               m.ASOnly,
		Score:                int32(m.Score),
		Latitude:             m.Latitude,
		Longitude:            m.Longitude,
		ContinentCode:        m.ContinentCode,
		CountryCodes:         m.CountryCodes,
		ExcludedCountryCodes: m.ExcludedCountryCodes,
		Asnum:                uint32(m.Asnum),
		Comment:              m.Comment,
		Enabled:              m.Enabled,
		HttpUp:               m.HttpUp,
		HttpsUp:              m.HttpsUp,
		HttpDownReason:       m.HttpDownReason,
		HttpsDownReason:      m.HttpsDownReason,
		StateSince:           stateSince,
		AllowRedirects:       int32(m.AllowRedirects),
		LastSync:             lastSync,
		LastSuccessfulSync:   lastSuccessfulSync,
		LastModTime:          lastModTime,
	}, nil
}

func MirrorFromRPC(m *Mirror) (*mirrors.Mirror, error) {
	stateSince, err := ptypes.Timestamp(m.StateSince)
	if err != nil {
		return nil, err
	}
	lastSync, err := ptypes.Timestamp(m.LastSync)
	if err != nil {
		return nil, err
	}
	lastSuccessfulSync, err := ptypes.Timestamp(m.LastSuccessfulSync)
	if err != nil {
		return nil, err
	}
	lastModTime, err := ptypes.Timestamp(m.LastModTime)
	if err != nil {
		return nil, err
	}
	return &mirrors.Mirror{
		ID:                   int(m.ID),
		Name:                 m.Name,
		HttpURL:              m.HttpURL,
		RsyncURL:             m.RsyncURL,
		FtpURL:               m.FtpURL,
		SponsorName:          m.SponsorName,
		SponsorURL:           m.SponsorURL,
		SponsorLogoURL:       m.SponsorLogoURL,
		AdminName:            m.AdminName,
		AdminEmail:           m.AdminEmail,
		CustomData:           m.CustomData,
		ContinentOnly:        m.ContinentOnly,
		CountryOnly:          m.CountryOnly,
		ASOnly:               m.ASOnly,
		Score:                int(m.Score),
		Latitude:             m.Latitude,
		Longitude:            m.Longitude,
		ContinentCode:        m.ContinentCode,
		CountryCodes:         m.CountryCodes,
		ExcludedCountryCodes: m.ExcludedCountryCodes,
		Asnum:                uint(m.Asnum),
		Comment:              m.Comment,
		Enabled:              m.Enabled,
		HttpUp:               m.HttpUp,
		HttpsUp:              m.HttpsUp,
		HttpDownReason:       m.HttpDownReason,
		HttpsDownReason:      m.HttpsDownReason,
		StateSince:           mirrors.Time{}.FromTime(stateSince),
		AllowRedirects:       mirrors.Redirects(m.AllowRedirects),
		LastSync:             mirrors.Time{}.FromTime(lastSync),
		LastSuccessfulSync:   mirrors.Time{}.FromTime(lastSuccessfulSync),
		LastModTime:          mirrors.Time{}.FromTime(lastModTime),
	}, nil
}
