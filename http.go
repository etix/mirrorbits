// Copyright (c) 2014 Ludovic Fauvet
// Licensed under the MIT license

package main

import (
	"encoding/json"
	"fmt"
	"github.com/etix/stoppableListener"
	"github.com/garyburd/redigo/redis"
	"html/template"
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
	templates  Templates
	SListener  *stoppableListener.StoppableListener
	stats      *Stats
	cache      *Cache
	engine     MirrorSelection
	Restarting bool
}

type Templates struct {
	mirrorlist  *template.Template
	mirrorstats *template.Template
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
	h.templates.mirrorlist = template.Must(h.LoadTemplates("mirrorlist"))
	h.templates.mirrorstats = template.Must(h.LoadTemplates("mirrorstats"))
	h.cache = cache
	h.stats = NewStats(redis)
	h.engine = DefaultEngine{}
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
	if t, err := h.LoadTemplates("mirrorlist"); err == nil {
		h.templates.mirrorlist = t //XXX lock needed?
	} else {
		log.Error("could not reload templates 'mirrorlist': %s", err.Error())
	}
	if t, err := h.LoadTemplates("mirrorstats"); err == nil {
		h.templates.mirrorstats = t //XXX lock needed?
	} else {
		log.Error("could not reload templates 'mirrorstats': %s", err.Error())
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
	ctx := NewContext(w, r, h.templates)
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

	mirrors, excluded, err := h.engine.Selection(ctx, h.cache, &fileInfo, clientInfo)

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
			http.Error(w, nerr.Error(), http.StatusInternalServerError)
		}
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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

// LoadTemplates pre-loads templates from the configured template directory
func (h *HTTP) LoadTemplates(name string) (t *template.Template, err error) {
	t = template.New("t")
	t.Funcs(template.FuncMap{
		"add":    add,
		"sizeof": readableSize,
	})
	t, err = t.ParseFiles(
		fmt.Sprintf("%s/base.html", GetConfig().Templates),
		fmt.Sprintf("%s/%s.html", GetConfig().Templates, name))
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
			http.Error(w, err.Error(), http.StatusInternalServerError)
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
			http.Error(w, err.Error(), http.StatusInternalServerError)
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
	err = ctx.Templates().mirrorstats.ExecuteTemplate(ctx.ResponseWriter(), "base", MirrorStatsPage{results, mirrors})
	if err != nil {
		log.Error("HTTP error: %s", err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}
