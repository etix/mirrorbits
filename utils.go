// Copyright (c) 2014 Ludovic Fauvet
// Licensed under the MIT license

package main

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"github.com/garyburd/redigo/redis"
	"io"
	"math"
	"os"
	"strings"
	"time"
)

const (
	DegToRad = 0.017453292519943295769236907684886127134428718885417 // N[Pi/180, 50]
	RadToDeg = 57.295779513082320876798154814105170332405472466564   // N[180/Pi, 50]
)

var welcome = ` _______ __                        __     __ __
|   |   |__|.----.----.-----.----.|  |--.|__|  |_.-----.
|       |  ||   _|   _|  _  |   _||  _  ||  |   _|__ --|
|__|_|__|__||__| |__| |_____|__|  |_____||__|____|_____|
                                                        `

func enableMirror(r *redisobj, id string) error {
	return setMirrorEnabled(r, id, true)
}

func disableMirror(r *redisobj, id string) error {
	return setMirrorEnabled(r, id, false)
}

func setMirrorEnabled(r *redisobj, id string, state bool) error {
	conn := r.pool.Get()
	defer conn.Close()

	key := fmt.Sprintf("MIRROR_%s", id)
	_, err := conn.Do("HMSET", key, "enabled", state)

	// Publish update
	conn.Do("PUBLISH", MIRROR_UPDATE, id)

	return err
}

func markMirrorUp(r *redisobj, id string) error {
	return setMirrorState(r, id, true, "")
}

func markMirrorDown(r *redisobj, id string, reason string) error {
	return setMirrorState(r, id, false, reason)
}

func setMirrorState(r *redisobj, id string, state bool, reason string) error {
	conn := r.pool.Get()
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
		conn.Do("PUBLISH", MIRROR_UPDATE, id)
	}

	return err
}

// Add a trailing slash to the URL
func normalizeURL(url string) string {
	if url != "" && !strings.HasSuffix(url, "/") {
		url += "/"
	}
	return url
}

// Generate a human readable sha1 hash of the given file path
func hashFile(path string) (hash string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	sha1Hash := sha1.New()
	_, err = io.Copy(sha1Hash, reader)
	if err != nil {
		return
	}
	hash = hex.EncodeToString(sha1Hash.Sum(nil))
	return
}

// Return the distance in km between two coordinates
func getDistanceKm(lat1, lon1, lat2, lon2 float32) float32 {
	var R float32 = 6371 // radius of the earth in Km
	dLat := (lat2 - lat1) * float32(DegToRad)
	dLon := (lon2 - lon1) * float32(DegToRad)
	a := math.Sin(float64(dLat/2))*math.Sin(float64(dLat/2)) + math.Cos(float64(lat1*DegToRad))*math.Cos(float64(lat2*DegToRad))*math.Sin(float64(dLon/2))*math.Sin(float64(dLon/2))

	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return R * float32(c)
}

func getMirrorMapUrl(mirrors Mirrors, clientInfo GeoIPRec) string {
	var buffer bytes.Buffer
	buffer.WriteString("//maps.googleapis.com/maps/api/staticmap?size=520x320&sensor=false&visual_refresh=true")

	if clientInfo.isValid() {
		buffer.WriteString(fmt.Sprintf("&markers=size:mid|color:red|%f,%f", clientInfo.Latitude, clientInfo.Longitude))
	}

	count := 1
	for i, mirror := range mirrors {
		if count > 9 {
			break
		}
		if i == 0 && clientInfo.isValid() {
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

func min(v1, v2 int) int {
	if v1 < v2 {
		return v1
	}
	return v2
}

func max(v1, v2 int) int {
	if v1 > v2 {
		return v1
	}
	return v2
}

func add(x, y int) int {
	return x + y
}

func version() string {
	return VERSION
}

// Return true is `a` is contained in `list`
// Warning: this is slow, don't use it for long datasets
func isInSlice(a string, list []string) bool {
	for _, b := range list {
		if b == a {
			return true
		}
	}
	return false
}

func isAdditionalCountry(clientInfo GeoIPRec, list []string) bool {
	if clientInfo.GeoIPRecord == nil {
		return false
	}
	for i, b := range list {
		if i > 0 && b == clientInfo.CountryCode {
			return true
		}
	}
	return false
}

func isPrimaryCountry(clientInfo GeoIPRec, list []string) bool {
	if clientInfo.GeoIPRecord == nil {
		return false
	}
	if len(list) > 0 && list[0] == clientInfo.CountryCode {
		return true
	}
	return false
}

// Return true if a stop has been requested
func isStopped(stop chan bool) bool {
	select {
	case <-stop:
		return true
	default:
		return false
	}
}

// Return a file size in a human readable form
func readableSize(value int64) string {
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
