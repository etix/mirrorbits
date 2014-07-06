// Copyright (c) 2014 Ludovic Fauvet
// Licensed under the MIT license

package main

import (
	"crypto/sha1"
	"fmt"
	"github.com/garyburd/redigo/redis"
	"io"
	"io/ioutil"
	"math"
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

func enableMirror(id string) error {
	return setMirrorEnabled(id, true)
}

func disableMirror(id string) error {
	return setMirrorEnabled(id, false)
}

func setMirrorEnabled(id string, state bool) error {
	r := NewRedis()
	conn, err := r.connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	key := fmt.Sprintf("MIRROR_%s", id)
	_, err = conn.Do("HMSET", key, "enabled", state)

	// Publish update
	conn.Do("PUBLISH", MIRROR_UPDATE, id)

	return err
}

func markMirrorUp(id string) error {
	return setMirrorState(id, true, "")
}

func markMirrorDown(id string, reason string) error {
	return setMirrorState(id, false, reason)
}

func setMirrorState(id string, state bool, reason string) error {
	r := NewRedis()
	conn, err := r.connect()
	if err != nil {
		return err
	}
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

func sha1File(file string) (string, error) {
	content, err := ioutil.ReadFile(file)
	if err != nil {
		return "", err
	}

	h := sha1.New()
	io.WriteString(h, string(content))

	return string(h.Sum(nil)), nil
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
