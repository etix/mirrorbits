// Copyright (c) 2014-2015 Ludovic Fauvet
// Licensed under the MIT license

package main

import (
	"encoding/json"
	"fmt"
	"github.com/garyburd/redigo/redis"
	"gopkg.in/tylerb/graceful.v1"
	"html/template"
	"math/rand"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// HTTP represents an instance of the HTTP webserver
type HTTP struct {
	geoip          *GeoIP
	redis          *redisobj
	templates      Templates
	Listener       *net.Listener
	server         *graceful.Server
	serverStopChan <-chan struct{}
	stats          *Stats
	cache          *Cache
	engine         MirrorSelection
	Restarting     bool
	stopped        bool
	stoppedMutex   sync.Mutex
}

// Templates is a struct embedding instances of the precompiled templates
type Templates struct {
	mirrorlist    *template.Template
	mirrorstats   *template.Template
	downloadstats *template.Template
}

// FileInfo is a struct embedding details about a file served by
// the redirector.
type FileInfo struct {
	Path    string    `redis:"-"`
	Size    int64     `redis:"size" json:",omitempty"`
	ModTime time.Time `redis:"modTime" json:",omitempty"`
	Sha1    string    `redis:"sha1" json:",omitempty"`
	Sha256  string    `redis:"sha256" json:",omitempty"`
	Md5     string    `redis:"md5" json:",omitempty"`
}

// Results is the resulting struct of a request and is
// used by the renderers to generate the final page.
type Results struct {
	FileInfo     FileInfo
	MapURL       string `json:"-"`
	IP           string
	ClientInfo   GeoIPRec
	MirrorList   Mirrors
	ExcludedList Mirrors `json:",omitempty"`
	Fallback     bool    `json:",omitempty"`
}

// HTTPServer is the constructor of the HTTP server
func HTTPServer(redis *redisobj, cache *Cache) *HTTP {
	h := new(HTTP)
	h.redis = redis
	h.geoip = NewGeoIP()
	h.templates.mirrorlist = template.Must(h.LoadTemplates("mirrorlist"))
	h.templates.mirrorstats = template.Must(h.LoadTemplates("mirrorstats"))
	h.templates.downloadstats = template.Must(h.LoadTemplates("downloadstats"))
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

// SetListener can be used to set a different listener that should be used by the
// HTTP server. This is primarily used during seamless binary upgrade.
func (h *HTTP) SetListener(l net.Listener) {
	h.Listener = &l
}

func (h *HTTP) Stop(timeout time.Duration) {
	/* Close the server and process remaining connections */
	h.stoppedMutex.Lock()
	defer h.stoppedMutex.Unlock()
	if h.stopped {
		return
	}
	h.stopped = true
	h.server.Stop(timeout)
}

// Terminate terminates the current HTTP server gracefully
func (h *HTTP) Terminate() {
	/* Wait for the server to stop */
	select {
	case <-h.serverStopChan:
	}
	/* Commit the latest recorded stats to the database */
	h.stats.Terminate()
}

func (h *HTTP) StopChan() <-chan struct{} {
	return h.serverStopChan
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
	if t, err := h.LoadTemplates("downloadstats"); err == nil {
		h.templates.downloadstats = t //XXX lock needed?
	} else {
		log.Error("could not reload templates 'downloadstats': %s", err.Error())
	}
}

// RunServer is the main function used to start the HTTP server
func (h *HTTP) RunServer() (err error) {
	// If listener isn't nil that means that we're running a seamless
	// binary upgrade and we have recovered an already running listener
	if h.Listener == nil {
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

	h.server = &graceful.Server{
		// http
		Server: &http.Server{
			Handler:        nil,
			ReadTimeout:    10 * time.Second,
			WriteTimeout:   10 * time.Second,
			MaxHeaderBytes: 1 << 20,
		},

		// graceful
		Timeout:          10 * time.Second,
		NoSignalHandling: true,
	}
	h.serverStopChan = h.server.StopChan()

	log.Info("Service listening on %s", GetConfig().ListenAddress)

	/* Serve until we receive a SIGTERM */
	return h.server.Serve(*h.Listener)
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
	case DOWNLOADSTATS:
		h.downloadStatsHandler(w, r, ctx)
	case CHECKSUM:
		h.checksumHandler(w, r, ctx)
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

	remoteIP := extractRemoteIP(r.Header.Get("X-Forwarded-For"))
	if len(remoteIP) == 0 {
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
	if _, ok := err.(net.Error); ok || len(mirrors) == 0 {
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
			http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		}
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	results := &Results{
		FileInfo:     fileInfo,
		MirrorList:   mirrors,
		ExcludedList: excluded,
		ClientInfo:   clientInfo,
		IP:           remoteIP,
		Fallback:     fallback,
	}

	var resultRenderer ResultsRenderer

	if ctx.IsMirrorlist() {
		resultRenderer = &MirrorListRenderer{}
	} else {
		switch GetConfig().OutputMode {
		case "json":
			resultRenderer = &JsonRenderer{}
		case "redirect":
			resultRenderer = &RedirectRenderer{}
		case "auto":
			accept := r.Header.Get("Accept")
			if strings.Index(accept, "application/json") >= 0 {
				resultRenderer = &JsonRenderer{}
			} else {
				resultRenderer = &RedirectRenderer{}
			}
		default:
			http.Error(w, "No page renderer", http.StatusInternalServerError)
			return
		}
	}

	status, err := resultRenderer.Write(ctx, results)
	if err != nil {
		http.Error(w, err.Error(), status)
	}

	if !ctx.IsMirrorlist() {
		logDownload(resultRenderer.Type(), status, results, err)
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
		"add":     add,
		"sizeof":  readableSize,
		"version": version,
	})
	t, err = t.ParseFiles(
		filepath.Clean(GetConfig().Templates+"/base.html"),
		filepath.Clean(fmt.Sprintf("%s/%s.html", GetConfig().Templates, name)))
	if err != nil {
		if e, ok := err.(*os.PathError); ok {
			log.Fatal(fmt.Sprintf("Cannot load template %s: %s", e.Path, e.Err.Error()))
		} else {
			log.Fatal(err.Error())
		}
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

type DownloadStats struct {
	Filename  string
	Downloads int64
}

type DownloadStatsPage struct {
	List   []*DownloadStats
	Period string
	Limit  int
	Month  string
	Today  string
	Path   string
}

func (h *HTTP) downloadStatsHandler(w http.ResponseWriter, r *http.Request, ctx *Context) {
	var results []*DownloadStats
	var output  []byte
	var filter  string
	var period  string
	var index   int64

	// parse query params
	req := strings.SplitN(ctx.QueryParam("downloadstats"), "-", 3)
	if req[0] != "" {
		for _, e := range req {
			if _, err := strconv.ParseInt(e, 10, 0); err != nil {
				http.Error(w, "Invalid period", http.StatusBadRequest)
				return
			}
		}
		period = strings.Replace(ctx.QueryParam("downloadstats"), "-", "_", 3)
	}

	format := "text"
	if ctx.QueryParam("format") == "json" {
		format = "json"
	}

	haveFilter := false
	if len(ctx.QueryParam("filter")) >= 1 {
		haveFilter = true
		filter = strings.Trim(ctx.QueryParam("filter"), " !#&%$*+'")
	}

	limit := 100
	haveLimit := true
	if ctx.QueryParam("limit") != "" {
		l, err := strconv.ParseInt(ctx.QueryParam("limit"), 0, 0)
		if err != nil || l < 0 {
			http.Error(w, "Invalid limit", http.StatusBadRequest)
			return
		}
		limit = int(l)
		if limit == 0 {
			haveLimit = false
		}
	}

	t0 := time.Now()
	rconn := h.redis.pool.Get()
	defer rconn.Close()

	// get stats array from redis
	var dkey string
	if len(period) >= 4 {
		dkey = fmt.Sprintf("STATS_FILE_%s", period)
	} else {
		dkey = "STATS_FILE"
	}
	v, err := redis.Strings(rconn.Do("HGETALL", dkey))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// generatate a map of our results, with download count as index
	var m = make(map[int64]string)
	for i := 0; i < len(v); i = i + 2 {
		index, _ = strconv.ParseInt(v[i+1], 10, 64)
		if haveFilter {
			if strings.Contains(v[i], filter) {
				m[index] = v[i]
			}
		} else {
			m[index] = v[i]
		}
	}
	v = nil

	// generate a sortable int64 array of our map indices
	dls := make([]int64, len(m))
	var i int64 = 0
	for k := range m {
		dls[i] = k
		i++
	}

	// sort the array in reverse order
	Int64Slice.Reverse(dls)
	log.Debug("Stats generation took %v", time.Now().Sub(t0))

	// construct final results
	t4 := time.Now()
	stop := 0
	for _, k := range dls {
		s := &DownloadStats{Downloads: k, Filename: m[k]}
		results = append(results, s)

		if haveLimit {
			stop++
			if stop >= limit {
				break
			}
		}
	}

	// output
	if format == "text" {
		if len(period) < 4 {
			period = "All time"
		}
		today := time.Now().Format("2006-01-02")
		month := time.Now().Format("2006-01")

		err = ctx.Templates().downloadstats.ExecuteTemplate(ctx.ResponseWriter(), "base",
			DownloadStatsPage{results, period, limit, month, today, GetConfig().DownloadStatsPath})
		if err != nil {
			log.Error("Error rendering downloadstats: %s", err.Error())
			http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
			return
		}
	} else {
		output, err = json.MarshalIndent(results, "", "    ")
		if err != nil {
			http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
			return
		}
		ctx.ResponseWriter().Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Write(output)
	}
	log.Debug("downloadStatsHandler: output took %v", time.Now().Sub(t4))

}

func (h *HTTP) checksumHandler(w http.ResponseWriter, r *http.Request, ctx *Context) {

	fileInfo, err := h.cache.GetFileInfo(r.URL.Path)
	if err == redis.ErrNil {
		http.NotFound(w, r)
		return
	} else if err != nil {
		log.Error("Error while fetching Fileinfo: %s", err.Error())
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	}

	var hash string

	if ctx.paramBool("md5") {
		hash = fileInfo.Md5
	} else if ctx.paramBool("sha1") {
		hash = fileInfo.Sha1
	} else if ctx.paramBool("sha256") {
		hash = fileInfo.Sha256
	}

	if len(hash) == 0 {
		http.Error(w, "Hash type not supported", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
	w.Write([]byte(fmt.Sprintf("%s  %s", hash, filepath.Base(fileInfo.Path))))

	return
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
