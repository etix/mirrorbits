// Copyright (c) 2014-2019 Ludovic Fauvet
// Licensed under the MIT license

package http

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"
	"time"

	. "github.com/etix/mirrorbits/config"
	"github.com/etix/mirrorbits/filesystem"
	"github.com/etix/mirrorbits/mirrors"
	"github.com/etix/mirrorbits/network"
	"github.com/etix/mirrorbits/utils"
)

var (
	ErrInvalidFileInfo = errors.New("Invalid file info (modtime is zero)")
)

type mirrorSelection interface {
	// Selection must return an ordered list of selected mirror,
	// a list of rejected mirrors and and an error code.
	Selection(*Context, *mirrors.Cache, *filesystem.FileInfo, network.GeoIPRecord) (mirrors.Mirrors, mirrors.Mirrors, error)
}

// DefaultEngine is the default algorithm used for mirror selection
type DefaultEngine struct{}

// Selection returns an ordered list of selected mirror, a list of rejected mirrors and and an error code
func (h DefaultEngine) Selection(ctx *Context, cache *mirrors.Cache, fileInfo *filesystem.FileInfo, clientInfo network.GeoIPRecord) (mlist mirrors.Mirrors, excluded mirrors.Mirrors, err error) {
	// Bail out early if we don't have valid file details
	if fileInfo.ModTime.IsZero() {
		err = ErrInvalidFileInfo
		return
	}

	// Prepare and return the list of all potential mirrors
	mlist, err = cache.GetMirrors(fileInfo.Path, clientInfo)
	if err != nil {
		return
	}

	// Filter the list of mirrors
	mlist, excluded, closestMirror, farthestMirror := Filter(mlist, ctx.SecureOption(), fileInfo, clientInfo)

	if !clientInfo.IsValid() {
		// Shuffle the list
		//XXX Should we use the fallbacks instead?
		for i := range mlist {
			j := rand.Intn(i + 1)
			mlist[i], mlist[j] = mlist[j], mlist[i]
		}

		// Shortcut
		if !ctx.IsMirrorlist() {
			// Reduce the number of mirrors to process
			mlist = mlist[:utils.Min(5, len(mlist))]
		}
		return
	}

	// We're not interested in divisions by zero
	if closestMirror == 0 {
		closestMirror = math.SmallestNonzeroFloat32
	}

	/* Weight distribution for random selection [Probabilistic weight] */

	// Compute score for each mirror and return the mirrors eligible for weight distribution.
	// This includes:
	// - mirrors found in a 1.5x (configurable) range from the closest mirror
	// - mirrors targeting the given country (as primary or secondary)
	// - mirrors being in the same AS number
	totalScore := 0
	baseScore := int(farthestMirror)
	weights := map[int]int{}
	for i := 0; i < len(mlist); i++ {
		m := &mlist[i]

		m.ComputedScore = baseScore - int(m.Distance) + 1

		if m.Distance <= closestMirror*GetConfig().WeightDistributionRange {
			score := (float32(baseScore) - m.Distance)
			if !network.IsPrimaryCountry(clientInfo, m.CountryFields) {
				score /= 2
			}
			m.ComputedScore += int(score)
		} else if network.IsPrimaryCountry(clientInfo, m.CountryFields) {
			m.ComputedScore += int(float32(baseScore) - (m.Distance * 5))
		} else if network.IsAdditionalCountry(clientInfo, m.CountryFields) {
			m.ComputedScore += int(float32(baseScore) - closestMirror)
		}

		if m.Asnum == clientInfo.ASNum {
			m.ComputedScore += baseScore / 2
		}

		floatingScore := float64(m.ComputedScore) + (float64(m.ComputedScore) * (float64(m.Score) / 100)) + 0.5

		// The minimum allowed score is 1
		m.ComputedScore = int(math.Max(floatingScore, 1))

		if m.ComputedScore > baseScore {
			// The weight must always be > 0 to not break the randomization below
			totalScore += m.ComputedScore - baseScore
			weights[m.ID] = m.ComputedScore - baseScore
		}
	}

	// Get the final number of mirrors selected for weight distribution
	selected := len(weights)

	// Sort mirrors by computed score
	sort.Sort(mirrors.ByComputedScore{Mirrors: mlist})

	if selected > 1 {

		if ctx.IsMirrorlist() {
			// Don't reorder the results, just set the percentage
			for i := 0; i < selected; i++ {
				id := mlist[i].ID
				for j := 0; j < len(mlist); j++ {
					if mlist[j].ID == id {
						mlist[j].Weight = float32(float64(weights[id]) * 100 / float64(totalScore))
						break
					}
				}
			}
		} else {
			// Randomize the order of the selected mirrors considering their weights
			weightedMirrors := make([]mirrors.Mirror, selected)
			rest := totalScore
			for i := 0; i < selected; i++ {
				var id int
				rv := rand.Int31n(int32(rest))
				s := 0
				for k, v := range weights {
					s += v
					if int32(s) > rv {
						id = k
						break
					}
				}
				for _, m := range mlist {
					if m.ID == id {
						m.Weight = float32(float64(weights[id]) * 100 / float64(totalScore))
						weightedMirrors[i] = m
						break
					}
				}
				rest -= weights[id]
				delete(weights, id)
			}

			// Replace the head of the list by its reordered counterpart
			mlist = append(weightedMirrors, mlist[selected:]...)

			// Reduce the number of mirrors to return
			v := math.Min(math.Min(5, float64(selected)), float64(len(mlist)))
			mlist = mlist[:int(v)]
		}
	} else if selected == 1 && len(mlist) > 0 {
		mlist[0].Weight = 100
	}
	return
}

// Filter mirror list, return the list of mirrors candidates for redirection,
// and the list of mirrors that were excluded. Also return the distance of the
// closest and farthest mirrors.
func Filter(mlist mirrors.Mirrors, secureOption SecureOption, fileInfo *filesystem.FileInfo, clientInfo network.GeoIPRecord) (accepted mirrors.Mirrors, excluded mirrors.Mirrors, closestMirror float32, farthestMirror float32) {
	// Check if this file is allowed to be outdated
	checkSize := true
	maxOutdated := time.Duration(0)
	config := GetConfig().AllowOutdatedFiles
	for _, c := range config {
		if strings.HasPrefix(fileInfo.Path, c.Prefix) {
			checkSize = false
			maxOutdated = time.Duration(c.Minutes) * time.Minute
			break
		}
	}

	accepted = make([]mirrors.Mirror, 0, len(mlist))
	excluded = make([]mirrors.Mirror, 0, len(mlist))

	for _, m := range mlist {
		// Is it enabled?
		if !m.Enabled {
			m.ExcludeReason = "Disabled"
			goto discard
		}

		// Is the procol requested supported by the mirror?
		// Is the mirror up for this protocol?
		switch secureOption {
		case WITHTLS:
			// HTTPS explicitly requested
			m.AbsoluteURL = ensureAbsolute(m.HttpURL, "https")
			httpsSupported := !strings.HasPrefix(m.HttpURL, "http://")
			if !httpsSupported {
				m.ExcludeReason = "Not HTTPS"
			} else if !m.HttpsUp {
				m.ExcludeReason = either(m.HttpsDownReason, "Down")
			} else {
				break
			}
			goto discard
		case WITHOUTTLS:
			// HTTP explicitly requested
			m.AbsoluteURL = ensureAbsolute(m.HttpURL, "http")
			httpSupported := !strings.HasPrefix(m.HttpURL, "https://")
			if !httpSupported {
				m.ExcludeReason = "Not HTTP"
			} else if !m.HttpUp {
				m.ExcludeReason = either(m.HttpDownReason, "Down")
			} else {
				break
			}
			goto discard
		default:
			// Any protocol will do - favor HTTPS if avail
			var httpReason, httpsReason string

			m.AbsoluteURL = ensureAbsolute(m.HttpURL, "https")
			httpsSupported := !strings.HasPrefix(m.HttpURL, "http://")
			if !httpsSupported {
				httpsReason = "Not HTTPS"
			} else if !m.HttpsUp {
				httpsReason = either(m.HttpsDownReason, "Down")
			} else {
				break
			}

			m.AbsoluteURL = ensureAbsolute(m.HttpURL, "http")
			httpSupported := !strings.HasPrefix(m.HttpURL, "https://")
			if !httpSupported {
				httpReason = "Not HTTP"
			} else if !m.HttpUp {
				httpReason = either(m.HttpDownReason, "Down")
			} else {
				break
			}

			if httpReason == httpsReason {
				m.ExcludeReason = httpReason
			} else {
				m.ExcludeReason = httpReason + " / " + httpsReason
			}
			goto discard
		}

		// Is it the same size / modtime as source?
		if m.FileInfo != nil {
			if checkSize && m.FileInfo.Size != fileInfo.Size {
				m.ExcludeReason = "File size mismatch"
				goto discard
			}
			if !m.FileInfo.ModTime.IsZero() {
				mModTime := m.FileInfo.ModTime
				if GetConfig().FixTimezoneOffsets {
					offset := time.Duration(m.TZOffset) * time.Millisecond
					mModTime = mModTime.Add(offset)
				}
				precision := m.LastSuccessfulSyncPrecision.Duration()
				mModTime = mModTime.Truncate(precision)
				lModTime := fileInfo.ModTime.Truncate(precision)
				delta := lModTime.Sub(mModTime)
				if delta < 0 || delta > maxOutdated {
					m.ExcludeReason = fmt.Sprintf("Mod time mismatch (diff: %s)", delta)
					goto discard
				}
			}
		}
		// Is it configured to serve its continent only?
		if m.ContinentOnly {
			if !clientInfo.IsValid() || clientInfo.ContinentCode != m.ContinentCode {
				m.ExcludeReason = "Continent only"
				goto discard
			}
		}
		// Is it configured to serve its country only?
		if m.CountryOnly {
			if !clientInfo.IsValid() || !utils.IsInSlice(clientInfo.CountryCode, m.CountryFields) {
				m.ExcludeReason = "Country only"
				goto discard
			}
		}
		// Is it in the same AS number?
		if m.ASOnly {
			if !clientInfo.IsValid() || clientInfo.ASNum != m.Asnum {
				m.ExcludeReason = "AS only"
				goto discard
			}
		}
		// Is the user's country code allowed on this mirror?
		if clientInfo.IsValid() && utils.IsInSlice(clientInfo.CountryCode, m.ExcludedCountryFields) {
			m.ExcludeReason = "User's country restriction"
			goto discard
		}
		// Keep track of the closest and farthest mirrors
		if len(accepted) == 0 {
			closestMirror = m.Distance
		} else if m.Distance < closestMirror {
			closestMirror = m.Distance
		}
		if m.Distance > farthestMirror {
			farthestMirror = m.Distance
		}
		accepted = append(accepted, m)
		continue
	discard:
		excluded = append(excluded, m)
	}

	return
}

// ensureAbsolute returns the url 'as is' if it's absolute (ie. it starts with
// a scheme), otherwise it prepends '<scheme>://' and returns the result.
func ensureAbsolute(url string, scheme string) string {
	if utils.HasAnyPrefix(url, "http://", "https://") {
		return url
	}
	return scheme + "://" + url
}

// either returns s if it's not empty, d otherwise
func either(s string, d string) string {
	if s != "" {
		return s
	}
	return d
}
