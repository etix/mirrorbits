// Copyright (c) 2014-2017 Ludovic Fauvet
// Licensed under the MIT license

package utils

import (
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/etix/mirrorbits/core"
	"github.com/etix/mirrorbits/network"
)

const (
	// DegToRad is a constant to convert degrees to radians
	DegToRad = 0.017453292519943295769236907684886127134428718885417 // N[Pi/180, 50]
	// RadToDeg is a constant to convert radians to degrees
	RadToDeg = 57.295779513082320876798154814105170332405472466564 // N[180/Pi, 50]
)

// NormalizeURL adds a trailing slash to the URL
func NormalizeURL(url string) string {
	if url != "" && !strings.HasSuffix(url, "/") {
		url += "/"
	}
	return url
}

// GetDistanceKm returns the distance in km between two coordinates
func GetDistanceKm(lat1, lon1, lat2, lon2 float32) float32 {
	var R float32 = 6371 // radius of the earth in Km
	dLat := (lat2 - lat1) * float32(DegToRad)
	dLon := (lon2 - lon1) * float32(DegToRad)
	a := math.Sin(float64(dLat/2))*math.Sin(float64(dLat/2)) + math.Cos(float64(lat1*DegToRad))*math.Cos(float64(lat2*DegToRad))*math.Sin(float64(dLon/2))*math.Sin(float64(dLon/2))

	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return R * float32(c)
}

// Min returns the smallest of the two values
func Min(v1, v2 int) int {
	if v1 < v2 {
		return v1
	}
	return v2
}

// Max returns the highest of the two values
func Max(v1, v2 int) int {
	if v1 > v2 {
		return v1
	}
	return v2
}

// Add does a simple addition
func Add(x, y int) int {
	return x + y
}

// Version returns the version as a string
func Version() string {
	return core.VERSION
}

// Hostname return the host name as a string
func Hostname() string {
	hostname, _ := os.Hostname()
	return hostname
}

// IsInSlice returns true is `a` is contained in `list`
// Warning: this is slow, don't use it for long datasets
func IsInSlice(a string, list []string) bool {
	for _, b := range list {
		if b == a {
			return true
		}
	}
	return false
}

// IsAdditionalCountry returns true if the clientInfo country is in list
func IsAdditionalCountry(clientInfo network.GeoIPRecord, list []string) bool {
	if !clientInfo.IsValid() {
		return false
	}
	for i, b := range list {
		if i > 0 && b == clientInfo.CountryCode {
			return true
		}
	}
	return false
}

// IsPrimaryCountry returns true if the clientInfo country is the primary country
func IsPrimaryCountry(clientInfo network.GeoIPRecord, list []string) bool {
	if !clientInfo.IsValid() {
		return false
	}
	if len(list) > 0 && list[0] == clientInfo.CountryCode {
		return true
	}
	return false
}

// IsStopped returns true if a stop has been requested
func IsStopped(stop chan bool) bool {
	select {
	case <-stop:
		return true
	default:
		return false
	}
}

// ReadableSize returns a file size in a human readable form
func ReadableSize(value int64) string {
	units := []string{"bytes", "KB", "MB", "GB", "TB"}

	v := float64(value)

	for _, u := range units {
		if v < 1024 || u == "TB" {
			return fmt.Sprintf("%3.1f %s", v, u)
		}
		v /= 1024
	}
	return ""
}

// ElapsedSec returns true if lastTimestamp + elapsed time is in the past
func ElapsedSec(lastTimestamp int64, elapsedTime int64) bool {
	if lastTimestamp+elapsedTime < time.Now().UTC().Unix() {
		return true
	}
	return false
}

// Plural returns a single 's' if there are more than one value
func Plural(value interface{}) string {
	n, ok := value.(int)
	if ok && n > 1 || n < -1 {
		return "s"
	}
	return ""
}

// ConcatURL concatenate the url and path
func ConcatURL(url, path string) string {
	if strings.HasSuffix(url, "/") && strings.HasPrefix(path, "/") {
		return url[:len(url)-1] + path
	}
	if !strings.HasSuffix(url, "/") && !strings.HasPrefix(path, "/") {
		return url + "/" + path
	}
	return url + path
}

// FormattedDateUTC returns the date formatted as RFC1123
func FormattedDateUTC(t time.Time) string {
	return t.UTC().Format(time.RFC1123)
}

// TimeKeyCoverage returns a slice of strings covering the date range
// used in the redis backend.
func TimeKeyCoverage(start, end time.Time) (dates []string) {
	if start.Day() == end.Day() && start.Month() == end.Month() && start.Year() == end.Year() {
		dates = append(dates, start.Format("2006_01_02"))
		return
	}

	if start.Day() != 1 {
		month := start.Month()
		for {
			if start.Month() != month || start.Equal(end) {
				break
			}
			dates = append(dates, start.Format("2006_01_02"))
			start = start.AddDate(0, 0, 1)
		}
	}

	for {
		tmpyear := time.Date(start.Year()+1, 1, 1, 0, 0, 0, 0, start.Location())
		tmpmonth := time.Date(start.Year(), start.Month()+1, 1, 0, 0, 0, 0, start.Location())
		if start.Day() == 1 && start.Month() == 1 && (tmpyear.Before(end) || tmpyear.Equal(end)) {
			dates = append(dates, start.Format("2006"))
			start = tmpyear
		} else if tmpmonth.Before(end) || tmpmonth.Equal(end) {
			dates = append(dates, start.Format("2006_01"))
			start = tmpmonth
		} else {
			break
		}
	}

	for {
		if start.AddDate(0, 0, 1).After(end) {
			break
		}
		dates = append(dates, start.Format("2006_01_02"))
		start = start.AddDate(0, 0, 1)
	}

	return
}

// FuzzyTimeStr returns the duration as fuzzy time
func FuzzyTimeStr(duration time.Duration) string {
	hours := duration.Hours()
	minutes := duration.Minutes()

	if int(minutes) == 0 {
		return "up-to-date"
	}

	if minutes < 0 {
		return "in the future"
	}

	if hours < 1 {
		return fmt.Sprintf("%d minute%s ago", int(duration.Minutes()), Plural(int(duration.Minutes())))
	}

	if hours/24 < 1 {
		return fmt.Sprintf("%d hour%s ago", int(hours), Plural(int(hours)))
	}

	if hours/24/365 > 1 {
		return fmt.Sprintf("%d year%s ago", int(hours/24/365), Plural(int(hours/24/365)))
	}

	return fmt.Sprintf("%d day%s ago", int(hours/24), Plural(int(hours/24)))
}

// SanitizeLocationCodes sanitizes the given location codes
func SanitizeLocationCodes(input string) string {
	input = strings.Replace(input, ",", " ", -1)
	ccodes := strings.Fields(input)
	output := ""
	for _, c := range ccodes {
		output += strings.ToUpper(c) + " "
	}
	return strings.TrimRight(output, " ")
}
