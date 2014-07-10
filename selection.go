// Copyright (c) 2014 Ludovic Fauvet
// Licensed under the MIT license

package main

import (
	"math"
	"math/rand"
	"sort"
	"strings"
)

type MirrorSelection interface {
	// Selection must return an ordered list of selected mirror,
	// a list of rejected mirrors and and an error code.
	Selection(*Context, *Cache, *FileInfo, GeoIPRec) (Mirrors, Mirrors, error)
}

// DefaultEngine is the default algorithm used for mirror selection
type DefaultEngine struct{}

func (h DefaultEngine) Selection(ctx *Context, cache *Cache, fileInfo *FileInfo, clientInfo GeoIPRec) (mirrors Mirrors, excluded Mirrors, err error) {
	// Get details about the requested file
	*fileInfo, err = cache.GetFileInfo(fileInfo.Path)
	if err != nil {
		return
	}

	// Prepare and return the list of all potential mirrors
	mirrors, err = cache.GetMirrors(fileInfo.Path, clientInfo)
	if err != nil {
		return
	}

	// Filter
	safeIndex := 0
	sameCountry := 0
	excluded = make([]Mirror, 0, len(mirrors))
	var closestMirror float32
	for i, m := range mirrors {
		// Does it support http? Is it well formated?
		if !strings.HasPrefix(m.HttpURL, "http://") {
			m.ExcludeReason = "Invalid URL"
			goto delete
		}
		// Is it enabled?
		if !m.Enabled {
			if m.ExcludeReason == "" {
				m.ExcludeReason = "Disabled"
			}
			goto delete
		}
		// Is it up?
		if !m.Up {
			if m.ExcludeReason == "" {
				m.ExcludeReason = "Down"
			}
			goto delete
		}
		// Is it the same size as source?
		if m.FileInfo != nil {
			if m.FileInfo.Size != fileInfo.Size {
				m.ExcludeReason = "File size mismatch"
				goto delete
			}
		}
		// Is it configured to serve its continent only?
		if m.ContinentOnly {
			if !clientInfo.isValid() || clientInfo.ContinentCode != m.ContinentCode {
				m.ExcludeReason = "Continent only"
				goto delete
			}
		}
		// Is it configured to serve its country only?
		if m.CountryOnly {
			if !clientInfo.isValid() || !isInSlice(clientInfo.CountryCode, m.CountryFields) {
				m.ExcludeReason = "Country only"
				goto delete
			}
		}
		// Is it in the same AS number?
		if m.ASOnly {
			if !clientInfo.isValid() || clientInfo.ASNum != m.Asnum {
				m.ExcludeReason = "AS only"
				goto delete
			}
		}
		if safeIndex == 0 {
			closestMirror = m.Distance
		} else if closestMirror > m.Distance {
			closestMirror = m.Distance
		}
		if isPrimaryCountry(clientInfo.CountryCode, m.CountryFields) {
			sameCountry++
		}
		mirrors[safeIndex] = mirrors[i]
		safeIndex++
		continue
	delete:
		excluded = append(excluded, m)
	}

	// Reduce the slice to its new size
	mirrors = mirrors[:safeIndex]

	// Sort by distance, ASNum and additional countries
	sort.Sort(ByRank{mirrors, clientInfo})

	if !clientInfo.isValid() {
		// Shortcut
		if !ctx.IsMirrorlist() {
			// Reduce the number of mirrors to process
			mirrors = mirrors[:min(5, len(mirrors))]
		}
		return
	}

	/* Weight distribution for random selection [Probabilistic weight] */

	// Compute weights for each mirror and return the mirrors eligible for weight distribution.
	// This includes:
	// - mirrors found in a 1.5x (configurable) range from the closest mirror
	// - mirrors targeting the given country (as primary or secondary country)
	weights := map[string]int{}
	boostWeights := map[string]int{}
	var (
		lastDistance       float32 = -1
		lastSelectionScore         = 0
		lastIsSelected             = false
		totalScore                 = 0
		lowestScore                = 0
		totalSelected              = 0
		relmax                     = len(mirrors)
	)
	for i := 0; i < len(mirrors); i++ {
		m := &mirrors[i]
		boost := false
		boostPoints := len(mirrors) - i

		if i == 0 {
			boost = true
			boostPoints += relmax
			lowestScore = boostPoints
		} else if m.Distance == lastDistance {
			boostPoints = lastSelectionScore
			boost = lastIsSelected
		} else if isPrimaryCountry(clientInfo.CountryCode, m.CountryFields) ||
			m.Distance <= closestMirror*GetConfig().WeightDistributionRange {
			boostPoints += int(float64(relmax) - float64(m.Distance/closestMirror)*float64(sameCountry))
			boost = true
		} else if isAdditionalCountry(clientInfo.CountryCode, m.CountryFields) {
			boostPoints += relmax / 2
			boost = true
		}

		if m.Asnum == clientInfo.ASNum {
			boostPoints += relmax / 2
			boost = true
		}

		lastDistance = m.Distance
		lastSelectionScore = boostPoints
		lastIsSelected = boost
		boostPoints += int(float64(boostPoints)*(float64(m.Score)/100) + 0.5)
		if boostPoints < 1 {
			boostPoints = 1
		}
		if boost == true && boostPoints < lowestScore {
			lowestScore = boostPoints
		}
		if boost == true && boostPoints >= lowestScore {
			boostWeights[m.ID] = boostPoints
			totalScore += boostPoints
			totalSelected++
		}
		weights[m.ID] = boostPoints
	}

	// Sort all mirrors by weight
	sort.Sort(ByWeight{mirrors, weights})

	// If mirrorlist is not requested we can discard most mirrors to
	// improve the processing speed.
	if !ctx.IsMirrorlist() {
		// Reduce the number of mirrors to process
		v := math.Min(math.Max(5, float64(totalSelected)), float64(len(mirrors)))
		mirrors = mirrors[:int(v)]
	}

	if totalSelected > 1 {
		// Randomize the order of the selected mirrors considering their weights
		weightedMirrors := make([]Mirror, totalSelected)
		rest := totalScore
		for i := 0; i < totalSelected; i++ {
			var id string
			rv := rand.Int31n(int32(rest))
			s := 0
			for k, v := range boostWeights {
				s += v
				if int32(s) > rv {
					id = k
					break
				}
			}
			for _, m := range mirrors {
				if m.ID == id {
					m.Weight = int(float64(boostWeights[id])*100/float64(totalScore) + 0.5)
					weightedMirrors[i] = m
					break
				}
			}
			rest -= boostWeights[id]
			delete(boostWeights, id)
		}

		// Replace the head of the list by its reordered counterpart
		mirrors = append(weightedMirrors, mirrors[totalSelected:]...)
	} else if totalSelected == 1 && len(mirrors) > 0 {
		mirrors[0].Weight = 100
	}
	return
}
