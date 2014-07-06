// Copyright (c) 2014 Ludovic Fauvet
// Licensed under the MIT license

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/etix/stoppableListener"
	"github.com/garyburd/redigo/redis"
	"html/template"
	"math"
	"math/rand"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

type HTTP struct {
	geoip      *GeoIP
	redis      *redisobj
	template   *template.Template
	SListener  *stoppableListener.StoppableListener
	stats      *Stats
	cache      *Cache
	Restarting bool
}

type FileInfo struct {
	Path    string    `redis:"-"`
	Size    int64     `redis:"size" json:",omitempty"`
	ModTime time.Time `redis:"modTime" json:",omitempty"`
	Sha1    string    `redis:"sha1" json:",omitempty"`
}

type MirrorlistPage struct {
	FileInfo     FileInfo
	MapURL       string `json:"-"`
	IP           string
	ClientInfo   GeoIPRec
	MirrorList   Mirrors
	ExcludedList Mirrors `json:",omitempty"`
	Fallback     bool    `json:",omitempty"`
}

func HTTPServer(redis *redisobj, cache *Cache) *HTTP {
	h := new(HTTP)
	h.redis = redis
	h.geoip = NewGeoIP()
	h.template = template.Must(h.LoadTemplates())
	h.cache = cache
	h.stats = NewStats(redis)
	http.Handle("/", NewGzipHandler(h.requestDispatcher))

	// Load the GeoIP database
	if err := h.geoip.LoadGeoIP(); err != nil {
		log.Critical("Can't load the GeoIP databases: %v", err)
		if len(GetConfig().Fallbacks) > 0 {
			log.Warning("All requests will be served by the backup mirrors!")
		} else {
			log.Error("Please configure fallback mirrors!")
		}
	}

	// Initialize the random number generator
	rand.Seed(time.Now().UnixNano())
	return h
}

func (h *HTTP) SetListener(l net.Listener) {
	/* Make the listener stoppable to be able to shutdown the server gracefully */
	h.SListener = stoppableListener.Handle(l)
}

func (h *HTTP) Terminate() {
	/* Close the listener, killing all active connections */
	h.SListener.Close()
	/* Commit the latest recorded stats to the database */
	h.stats.Terminate()
}

// Reload the configuration
func (h *HTTP) Reload() {
	// Reload the templates
	if t, err := h.LoadTemplates(); err == nil {
		h.template = t //XXX lock needed?
	} else {
		log.Error("could not reload templates: %s", err.Error())
	}
}

func (h *HTTP) RunServer() (err error) {
	// If SListener isn't nil that means that we're running a seamless
	// binary upgrade and we have recovered an already running listener
	if h.SListener == nil {
		proto := "tcp"
		address := GetConfig().ListenAddress
		if strings.HasPrefix(address, "unix:") {
			proto = "unix"
			address = strings.TrimPrefix(address, "unix:")
		}
		listener, err := net.Listen(proto, address)
		if err != nil {
			log.Fatal("Listen: ", err)
		}
		h.SetListener(listener)
	}

	server := &http.Server{
		Handler:        nil,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	fmt.Println("Service listening on", GetConfig().ListenAddress)

	/* Serve until we receive a SIGTERM */
	return server.Serve(h.SListener)
}

func (h *HTTP) requestDispatcher(w http.ResponseWriter, r *http.Request) {
	ctx := NewContext(w, r, h.template)
	w.Header().Set("Server", "Mirrorbits/"+VERSION)

	switch ctx.Type() {
	case MIRRORLIST:
		fallthrough
	case STANDARD:
		h.mirrorHandler(w, r, ctx)
	case MIRRORSTATS:
		h.mirrorStatsHandler(w, r, ctx)
	case FILESTATS:
		h.fileStatsHandler(w, r, ctx)
	}
}

func (h *HTTP) mirrorHandler(w http.ResponseWriter, r *http.Request, ctx *Context) {
	//XXX it would be safer to recover in case of panic

	// Check if the file exists in the local repository
	if _, err := os.Stat(GetConfig().Repository + r.URL.Path); err != nil {
		http.NotFound(w, r)
		return
	}

	fileInfo := FileInfo{
		Path: r.URL.Path,
	}

	remoteIP := r.Header.Get("X-Forwarded-For")
	if remoteIP == "" {
		remoteIP = remoteIpFromAddr(r.RemoteAddr)
	}

	if ctx.IsMirrorlist() {
		fromip := ctx.QueryParam("fromip")
		if net.ParseIP(fromip) != nil {
			remoteIP = fromip
		}
	}

	clientInfo := h.geoip.GetInfos(remoteIP) //TODO return a pointer?

	mirrors, excluded, err := h.mirrorSelection(ctx, &fileInfo, clientInfo)

	/* Handle errors */
	fallback := false
	if nerr, ok := err.(net.Error); ok || len(mirrors) == 0 {
		/* Handle fallbacks */
		fallbacks := GetConfig().Fallbacks
		if len(fallbacks) > 0 {
			fallback = true
			for i, f := range fallbacks {
				mirrors = append(mirrors, Mirror{
					ID:            fmt.Sprintf("fallback%d", i),
					HttpURL:       f.Url,
					CountryCodes:  strings.ToUpper(f.CountryCode),
					CountryFields: []string{strings.ToUpper(f.CountryCode)},
					ContinentCode: strings.ToUpper(f.ContinentCode)})
			}
			sort.Sort(ByRank{mirrors, clientInfo})
		} else {
			// No fallback in stock, there's nothing else we can do
			http.Error(w, nerr.Error(), 500)
		}
	} else if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	page := &MirrorlistPage{
		FileInfo:     fileInfo,
		MirrorList:   mirrors,
		ExcludedList: excluded,
		ClientInfo:   clientInfo,
		IP:           remoteIP,
		Fallback:     fallback,
	}

	var pageRenderer PageRenderer

	if ctx.IsMirrorlist() {
		pageRenderer = &MirrorListRenderer{}
	} else {
		switch GetConfig().OutputMode {
		case "json":
			pageRenderer = &JsonRenderer{}
		case "redirect":
			pageRenderer = &RedirectRenderer{}
		case "auto":
			accept := r.Header.Get("Accept")
			if strings.Index(accept, "application/json") >= 0 {
				pageRenderer = &JsonRenderer{}
			} else {
				pageRenderer = &RedirectRenderer{}
			}
		default:
			http.Error(w, "No page renderer", http.StatusInternalServerError)
			return
		}
	}

	status, err := pageRenderer.Write(ctx, page)

	if err != nil {
		http.Error(w, err.Error(), status)
	}

	if !ctx.IsMirrorlist() {
		logDownload(pageRenderer.Type(), status, page, err)
		if len(mirrors) > 0 {
			h.stats.CountDownload(mirrors[0], fileInfo)
		}
	}

	return
}

func (h *HTTP) mirrorSelection(ctx *Context, fileInfo *FileInfo, clientInfo GeoIPRec) (mirrors Mirrors, excluded Mirrors, err error) {
	rconn := h.redis.pool.Get()
	defer rconn.Close()

	// Get details about the requested file
	*fileInfo, err = h.cache.GetFileInfo(fileInfo.Path)
	if err != nil {
		return
	}

	// Prepare and return the list of all potential mirrors
	mirrors, err = h.cache.GetMirrors(fileInfo.Path, clientInfo)
	if err != nil {
		return
	}

	// Filter
	safeIndex := 0
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
	var lastDistance float32 = -1
	var lastBoostPoints = 0
	var lastIsBoost = false
	var totalBoost = 0
	var lowestBoost = 0
	var selected = 0
	var relmax = len(mirrors)
	for i := 0; i < len(mirrors); i++ {
		m := &mirrors[i]
		boost := false
		boostPoints := len(mirrors) - i

		if i == 0 {
			boost = true
			boostPoints += relmax
			lowestBoost = boostPoints
		} else if m.Distance == lastDistance {
			boostPoints = lastBoostPoints
			boost = lastIsBoost
		} else if m.Distance <= closestMirror*GetConfig().WeightDistributionRange {
			limit := float64(closestMirror) * float64(GetConfig().WeightDistributionRange)
			boostPoints += int((limit-float64(m.Distance))*float64(relmax)/limit + 0.5)
			boost = true
		} else if isInSlice(clientInfo.CountryCode, m.CountryFields) {
			boostPoints += relmax / 2
			boost = true
		}

		if m.Asnum == clientInfo.ASNum {
			boostPoints += relmax / 2
			boost = true
		}

		lastDistance = m.Distance
		lastBoostPoints = boostPoints
		lastIsBoost = boost
		boostPoints += int(float64(boostPoints)*(float64(m.Score)/100) + 0.5)
		if boostPoints < 1 {
			boostPoints = 1
		}
		if boost == true && boostPoints < lowestBoost {
			lowestBoost = boostPoints
		}
		if boost == true && boostPoints >= lowestBoost {
			boostWeights[m.ID] = boostPoints
			totalBoost += boostPoints
			selected++
		}
		weights[m.ID] = boostPoints
	}

	// Sort all mirrors by weight
	sort.Sort(ByWeight{mirrors, weights})

	// If mirrorlist is not requested we can discard most mirrors to
	// improve the processing speed.
	if !ctx.IsMirrorlist() {
		// Reduce the number of mirrors to process
		v := math.Min(math.Max(5, float64(selected)), float64(len(mirrors)))
		mirrors = mirrors[:int(v)]
	}

	if selected > 1 {
		// Randomize the order of the selected mirrors considering their weights
		weightedMirrors := make([]Mirror, selected)
		rest := totalBoost
		for i := 0; i < selected; i++ {
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
					m.Weight = int(float64(boostWeights[id])*100/float64(totalBoost) + 0.5)
					weightedMirrors[i] = m
					break
				}
			}
			rest -= boostWeights[id]
			delete(boostWeights, id)
		}

		// Replace the head of the list by its reordered counterpart
		mirrors = append(weightedMirrors, mirrors[selected:]...)
	} else if selected == 1 && len(mirrors) > 0 {
		mirrors[0].Weight = 100
	}
	return
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

// LoadTemplates pre-loads templates from the configured template directory
func (h *HTTP) LoadTemplates() (t *template.Template, err error) {
	t = template.New("t")
	t.Funcs(template.FuncMap{
		"add":    add,
		"sizeof": readableSize,
	})
	t, err = t.ParseGlob(fmt.Sprintf("%s/*.html", GetConfig().Templates))
	if err != nil {
		panic(err)
	}
	return t, err
}

type StatsFileNow struct {
	Today int64
	Month int64
	Year  int64
	Total int64
}

type StatsFilePeriod struct {
	Period    string
	Downloads int64
}

// See stats.go header for the storage structure
func (h *HTTP) fileStatsHandler(w http.ResponseWriter, r *http.Request, ctx *Context) {
	var output []byte

	rconn := h.redis.pool.Get()
	defer rconn.Close()

	req := strings.SplitN(ctx.QueryParam("stats"), "-", 3)

	// Sanity check
	for _, e := range req {
		if e == "" {
			continue
		}
		if _, err := strconv.ParseInt(e, 10, 0); err != nil {
			http.Error(w, "Invalid period", http.StatusBadRequest)
			return
		}
	}

	if len(req) == 0 || req[0] == "" {
		fkey := fmt.Sprintf("STATS_FILE_%s", time.Now().Format("2006_01_02"))

		rconn.Send("MULTI")

		for i := 0; i < 4; i++ {
			rconn.Send("HGET", fkey, r.URL.Path)
			fkey = fkey[:strings.LastIndex(fkey, "_")]
		}

		res, err := redis.Values(rconn.Do("EXEC"))

		if err != nil && err != redis.ErrNil {
			http.Error(w, err.Error(), 500)
			return
		}

		s := &StatsFileNow{}
		s.Today, _ = redis.Int64(res[0], err)
		s.Month, _ = redis.Int64(res[1], err)
		s.Year, _ = redis.Int64(res[2], err)
		s.Total, _ = redis.Int64(res[3], err)

		output, err = json.MarshalIndent(s, "", "    ")
	} else {
		// Generate the redis key
		dkey := "STATS_FILE_"
		for _, e := range req {
			dkey += fmt.Sprintf("%s_", e)
		}
		dkey = dkey[:len(dkey)-1]

		v, err := redis.Int64(rconn.Do("HGET", dkey, r.URL.Path))
		if err != nil && err != redis.ErrNil {
			http.Error(w, err.Error(), 500)
			return
		}
		s := &StatsFilePeriod{Period: ctx.QueryParam("stats"), Downloads: v}

		output, err = json.MarshalIndent(s, "", "    ")
	}

	w.Write(output)
}

type MirrorStats struct {
	ID        string
	Downloads int64
	Bytes     int64
}

type MirrorStatsPage struct {
	List       []MirrorStats
	MirrorList []Mirror
}

type ByDownloadNumbers struct {
	MirrorStatsSlice
}

func (b ByDownloadNumbers) Less(i, j int) bool {
	if b.MirrorStatsSlice[i].Downloads > b.MirrorStatsSlice[j].Downloads {
		return true
	}
	return false
}

type MirrorStatsSlice []MirrorStats

func (s MirrorStatsSlice) Len() int      { return len(s) }
func (s MirrorStatsSlice) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

func (h *HTTP) mirrorStatsHandler(w http.ResponseWriter, r *http.Request, ctx *Context) {

	rconn := h.redis.pool.Get()
	defer rconn.Close()

	// Get all mirrors ID
	mirrorsIDs, err := redis.Strings(rconn.Do("LRANGE", "MIRRORS", "0", "-1"))
	if err != nil {
		http.Error(w, "Cannot fetch the list of mirrors", http.StatusInternalServerError)
		return
	}

	// <dlstats>
	rconn.Send("MULTI")

	// Get all mirrors stats
	for _, id := range mirrorsIDs {
		today := time.Now().Format("2006_01_02")
		rconn.Send("HGET", "STATS_MIRROR_"+today, id)
		rconn.Send("HGET", "STATS_MIRROR_BYTES_"+today, id)
	}

	stats, err := redis.Values(rconn.Do("EXEC"))
	if err != nil {
		http.Error(w, "Cannot fetch stats", http.StatusInternalServerError)
		return
	}

	var results []MirrorStats
	var index int64
	for _, id := range mirrorsIDs {
		var downloads int64
		if v, _ := redis.String(stats[index], nil); v != "" {
			downloads, _ = strconv.ParseInt(v, 10, 64)
		}
		var bytes int64
		if v, _ := redis.String(stats[index+1], nil); v != "" {
			bytes, _ = strconv.ParseInt(v, 10, 64)
		}
		s := MirrorStats{
			ID:        id,
			Downloads: downloads,
			Bytes:     bytes,
		}
		results = append(results, s)
		index += 2
	}

	sort.Sort(ByDownloadNumbers{results})

	// </dlstats>
	// <map>

	var mirrors []Mirror
	mirrors = make([]Mirror, 0, len(mirrorsIDs))
	for _, mirrorID := range mirrorsIDs {
		var mirror Mirror
		reply, err := redis.Values(rconn.Do("HGETALL", fmt.Sprintf("MIRROR_%s", mirrorID)))
		if err != nil {
			continue
		}
		if len(reply) == 0 {
			err = redis.ErrNil
			continue
		}
		err = redis.ScanStruct(reply, &mirror)
		if err != nil {
			continue
		}
		mirror.CountryFields = strings.Fields(mirror.CountryCodes)
		mirrors = append(mirrors, mirror)
	}

	// </map>

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	err = ctx.Templates().ExecuteTemplate(ctx.ResponseWriter(), "mirrorstats", MirrorStatsPage{results, mirrors})
	if err != nil {
		log.Error("HTTP error: %s", err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}
