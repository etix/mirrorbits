// Copyright (c) 2014-2019 Ludovic Fauvet
// Licensed under the MIT license

package http

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

	systemd "github.com/coreos/go-systemd/daemon"
	. "github.com/etix/mirrorbits/config"
	"github.com/etix/mirrorbits/core"
	"github.com/etix/mirrorbits/database"
	"github.com/etix/mirrorbits/filesystem"
	"github.com/etix/mirrorbits/logs"
	"github.com/etix/mirrorbits/mirrors"
	"github.com/etix/mirrorbits/network"
	"github.com/etix/mirrorbits/utils"
	"github.com/gomodule/redigo/redis"
	"github.com/op/go-logging"
	"gopkg.in/tylerb/graceful.v1"
)

var (
	log = logging.MustGetLogger("main")
)

// HTTP represents an instance of the HTTP webserver
type HTTP struct {
	geoip          *network.GeoIP
	redis          *database.Redis
	templates      Templates
	Listener       *net.Listener
	server         *graceful.Server
	serverStopChan <-chan struct{}
	stats          *Stats
	cache          *mirrors.Cache
	engine         mirrorSelection
	Restarting     bool
	stopped        bool
	stoppedMutex   sync.Mutex
}

// Templates is a struct embedding instances of the precompiled templates
type Templates struct {
	*sync.RWMutex

	mirrorlist  *template.Template
	mirrorstats *template.Template
}

// HTTPServer is the constructor of the HTTP server
func HTTPServer(redis *database.Redis, cache *mirrors.Cache) *HTTP {
	h := new(HTTP)
	h.redis = redis
	h.geoip = network.NewGeoIP()
	h.templates.RWMutex = new(sync.RWMutex)
	h.templates.mirrorlist = template.Must(h.LoadTemplates("mirrorlist"))
	h.templates.mirrorstats = template.Must(h.LoadTemplates("mirrorstats"))
	h.cache = cache
	h.stats = NewStats(redis)
	h.engine = DefaultEngine{}
	http.Handle("/", NewGzipHandler(h.requestDispatcher))

	// Load the GeoIP databases
	if err := h.geoip.LoadGeoIP(); err != nil {
		if gerr, ok := err.(network.GeoIPError); ok {
			for _, e := range gerr.Errors {
				log.Critical(e.Error())
			}
			if gerr.IsFatal() {
				if len(GetConfig().Fallbacks) == 0 {
					log.Fatal("Can't load the GeoIP databases, please set a valid path in the mirrorbits configuration")
				} else {
					log.Critical("Can't load the GeoIP databases, all requests will be served by the fallback mirrors")
				}
			} else {
				log.Critical("One or more GeoIP database could not be loaded, service will run in degraded mode")
			}
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

// Stop gracefully stops the HTTP server with a timeout to let
// the remaining connections finish
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

// StopChan returns a channel that notifies when the server is stopped
func (h *HTTP) StopChan() <-chan struct{} {
	return h.serverStopChan
}

// Reload the configuration
func (h *HTTP) Reload() {
	// Reload the GeoIP database
	h.geoip.LoadGeoIP()

	// Reload the templates
	h.templates.Lock()
	if t, err := h.LoadTemplates("mirrorlist"); err == nil {
		h.templates.mirrorlist = t
	} else {
		log.Errorf("could not reload templates 'mirrorlist': %s", err.Error())
	}
	if t, err := h.LoadTemplates("mirrorstats"); err == nil {
		h.templates.mirrorstats = t
	} else {
		log.Errorf("could not reload templates 'mirrorstats': %s", err.Error())
	}
	h.templates.Unlock()
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

	log.Infof("Service listening on %s", GetConfig().ListenAddress)

	// Since main blocks here until completion, tell systemd we're ready.
	// This is a no-op if NOTIFY_SOCKET isn't set.
	if os.Getenv("NOTIFY_SOCKET") != "" {
		log.Debug("Notifying systemd of readiness")
		systemd.SdNotify(false, systemd.SdNotifyReady)
	}

	/* Serve until we receive a SIGTERM */
	return h.server.Serve(*h.Listener)
}

func (h *HTTP) requestDispatcher(w http.ResponseWriter, r *http.Request) {
	h.templates.RLock()
	ctx := NewContext(w, r, h.templates)
	h.templates.RUnlock()

	w.Header().Set("Server", "Mirrorbits/"+core.VERSION)

	switch ctx.Type() {
	case MIRRORLIST:
		fallthrough
	case STANDARD:
		h.mirrorHandler(w, r, ctx)
	case MIRRORSTATS:
		h.mirrorStatsHandler(w, r, ctx)
	case FILESTATS:
		h.fileStatsHandler(w, r, ctx)
	case CHECKSUM:
		h.checksumHandler(w, r, ctx)
	}
}

func (h *HTTP) mirrorHandler(w http.ResponseWriter, r *http.Request, ctx *Context) {
	//XXX it would be safer to recover in case of panic

	// Sanitize path
	urlPath, err := filesystem.EvaluateFilePath(GetConfig().Repository, r.URL.Path)
	if err != nil {
		if err == filesystem.ErrOutsideRepo {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}

	fileInfo := filesystem.NewFileInfo(urlPath)

	remoteIP := network.ExtractRemoteIP(r.Header.Get("X-Forwarded-For"))
	if len(remoteIP) == 0 {
		remoteIP = network.RemoteIPFromAddr(r.RemoteAddr)
	}

	if ctx.IsMirrorlist() {
		fromip := ctx.QueryParam("fromip")
		if net.ParseIP(fromip) != nil {
			remoteIP = fromip
		}
	}

	clientInfo := h.geoip.GetRecord(remoteIP) //TODO return a pointer?

	mlist, excluded, err := h.engine.Selection(ctx, h.cache, &fileInfo, clientInfo)

	/* Handle errors */
	fallback := false
	if _, ok := err.(net.Error); ok || len(mlist) == 0 {
		/* Handle fallbacks */
		fallbacks := GetConfig().Fallbacks
		if len(fallbacks) > 0 {
			fallback = true
			for i, f := range fallbacks {
				mlist = append(mlist, mirrors.Mirror{
					ID:            i * -1,
					Name:          fmt.Sprintf("fallback%d", i),
					HttpURL:       f.URL,
					CountryCodes:  strings.ToUpper(f.CountryCode),
					CountryFields: []string{strings.ToUpper(f.CountryCode)},
					ContinentCode: strings.ToUpper(f.ContinentCode)})
			}
			sort.Sort(mirrors.ByRank{Mirrors: mlist, ClientInfo: clientInfo})
		} else {
			// No fallback in stock, there's nothing else we can do
			http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
			return
		}
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	results := &mirrors.Results{
		FileInfo:     fileInfo,
		MirrorList:   mlist,
		ExcludedList: excluded,
		ClientInfo:   clientInfo,
		IP:           remoteIP,
		Fallback:     fallback,
		LocalJSPath:  GetConfig().LocalJSPath,
	}

	var resultRenderer resultsRenderer

	if ctx.IsMirrorlist() {
		resultRenderer = &MirrorListRenderer{}
	} else {
		switch GetConfig().OutputMode {
		case "json":
			resultRenderer = &JSONRenderer{}
		case "redirect":
			resultRenderer = &RedirectRenderer{}
		case "auto":
			accept := r.Header.Get("Accept")
			if strings.Index(accept, "application/json") >= 0 {
				resultRenderer = &JSONRenderer{}
			} else {
				resultRenderer = &RedirectRenderer{}
			}
		default:
			http.Error(w, "No page renderer", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Cache-Control", "private, no-cache")

	status, err := resultRenderer.Write(ctx, results)
	if err != nil {
		http.Error(w, err.Error(), status)
	}

	if !ctx.IsMirrorlist() {
		logs.LogDownload(resultRenderer.Type(), status, results, err)
		if len(mlist) > 0 {
			timeout := GetConfig().SameDownloadInterval
			if r.Header.Get("Range") == "" || timeout == 0 {
				h.stats.CountDownload(mlist[0], fileInfo)
			} else {
				downloaderID := remoteIP+"/"+r.Header.Get("User-Agent")
				hash := sha256.New()
				hash.Write([]byte(downloaderID))
				chk := hex.EncodeToString(hash.Sum(nil))

				rconn := h.redis.Get()
				defer rconn.Close()

				tempKey := "DOWNLOADED_"+chk+"_"+urlPath

				prev := ""
				if h.redis.IsAtLeastVersion("6.2.0") {
					// Get and set the key in one command.
					prev, _ = redis.String(rconn.Do("SET", tempKey, 1, "GET", "EX", timeout))
				} else {
					prev, _ = redis.String(rconn.Do("GET", tempKey))
				}
				if prev == "" {
					// Only count partial requests as a new download if
					// we haven't had any recently from the same client
					// (i.e. same (IP, user-agent) hash). This prevents
					// from counting multiple times a single client
					// downloading a single file in pieces, such as
					// torrent clients when files are used as web seeds.
					h.stats.CountDownload(mlist[0], fileInfo)
				}

				if ! h.redis.IsAtLeastVersion("6.2.0") {
					// Set the key anyway to reset the timer.
					rconn.Send("SET", tempKey, 1, "EX", timeout)
				}
			}
		}
	}

	return
}

// LoadTemplates pre-loads templates from the configured template directory
func (h *HTTP) LoadTemplates(name string) (t *template.Template, err error) {
	t = template.New("t")
	t.Funcs(template.FuncMap{
		"add":       utils.Add,
		"sizeof":    utils.ReadableSize,
		"version":   utils.Version,
		"hostname":  utils.Hostname,
		"concaturl": utils.ConcatURL,
		"dateutc":   utils.FormattedDateUTC,
		"iszero":    utils.IsZero,
	})
	t, err = t.ParseFiles(
		filepath.Clean(GetConfig().Templates+"/base.html"),
		filepath.Clean(fmt.Sprintf("%s/%s.html", GetConfig().Templates, name)))
	if err != nil {
		if e, ok := err.(*os.PathError); ok {
			log.Fatalf(fmt.Sprintf("Cannot load template %s: %s", e.Path, e.Err.Error()))
		} else {
			log.Fatal(err.Error())
		}
	}
	return t, err
}

// StatsFileNow is the structure containing the latest stats of a file
type StatsFileNow struct {
	Today int64
	Month int64
	Year  int64
	Total int64
}

// StatsFilePeriod is the structure containing the stats for the given period
type StatsFilePeriod struct {
	Period    string
	Downloads int64
}

// See stats.go header for the storage structure
func (h *HTTP) fileStatsHandler(w http.ResponseWriter, r *http.Request, ctx *Context) {
	var output []byte

	rconn := h.redis.Get()
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

func (h *HTTP) checksumHandler(w http.ResponseWriter, r *http.Request, ctx *Context) {

	// Sanitize path
	urlPath, err := filesystem.EvaluateFilePath(GetConfig().Repository, r.URL.Path)
	if err != nil {
		if err == filesystem.ErrOutsideRepo {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}

	// Get details about the requested file
	fileInfo, err := h.cache.GetFileInfo(urlPath)
	if err != nil {
		log.Errorf("Error while fetching Fileinfo: %s", err.Error())
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

// MirrorStats contains the stats of a given mirror
type MirrorStats struct {
	ID         int
	Name       string
	Downloads  int64
	Bytes      int64
	PercentD   float32
	PercentB   float32
	SyncOffset SyncOffset
	TZOffset   time.Duration
}

// SyncOffset contains the time offset between the mirror and the local repository
type SyncOffset struct {
	Valid         bool
	Value         int // in hours
	HumanReadable string
}

// MirrorStatsPage contains the values needed to generate the mirrorstats page
type MirrorStatsPage struct {
	List             []MirrorStats
	MirrorList       []mirrors.Mirror
	LocalJSPath      string
	HasTZAdjustement bool
}

// byDownloadNumbers is a sorting function
type byDownloadNumbers struct {
	mirrorStatsSlice
}

func (b byDownloadNumbers) Less(i, j int) bool {
	if b.mirrorStatsSlice[i].Downloads > b.mirrorStatsSlice[j].Downloads {
		return true
	}
	return false
}

// mirrorStatsSlice is a slice of MirrorStats
type mirrorStatsSlice []MirrorStats

func (s mirrorStatsSlice) Len() int      { return len(s) }
func (s mirrorStatsSlice) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

func (h *HTTP) mirrorStatsHandler(w http.ResponseWriter, r *http.Request, ctx *Context) {

	rconn := h.redis.Get()
	defer rconn.Close()

	// Get all mirrors ID
	mirrorsMap, err := h.redis.GetListOfMirrors()
	if err != nil {
		http.Error(w, "Cannot fetch the list of mirrors", http.StatusInternalServerError)
		return
	}

	var mirrorsIDs []int
	for id := range mirrorsMap {
		// We need a common order to iterate the
		// results from Redis.
		mirrorsIDs = append(mirrorsIDs, id)
	}

	rconn.Send("MULTI")

	// Get all mirrors stats
	for _, id := range mirrorsIDs {
		today := time.Now().UTC().Format("2006_01_02")
		rconn.Send("HGET", "STATS_MIRROR_"+today, id)
		rconn.Send("HGET", "STATS_MIRROR_BYTES_"+today, id)
	}

	stats, err := redis.Values(rconn.Do("EXEC"))
	if err != nil {
		http.Error(w, "Cannot fetch stats", http.StatusInternalServerError)
		return
	}

	var hasTZAdjustement bool
	var maxdownloads int64
	var maxbytes int64
	var results []MirrorStats
	var index int64
	mlist := make([]mirrors.Mirror, 0, len(mirrorsIDs))
	for _, id := range mirrorsIDs {
		mirror, err := h.cache.GetMirror(id)
		if err != nil {
			continue
		}
		mlist = append(mlist, mirror)

		var downloads int64
		if v, _ := redis.String(stats[index], nil); v != "" {
			downloads, _ = strconv.ParseInt(v, 10, 64)
		}
		var bytes int64
		if v, _ := redis.String(stats[index+1], nil); v != "" {
			bytes, _ = strconv.ParseInt(v, 10, 64)
		}

		if downloads > maxdownloads {
			maxdownloads = downloads
		}
		if bytes > maxbytes {
			maxbytes = bytes
		}

		var lastModTime time.Time

		if !mirror.LastModTime.IsZero() {
			lastModTime = mirror.LastModTime.Time
		}

		elapsed := time.Since(lastModTime)

		tzoffset, _ := time.ParseDuration(fmt.Sprintf("%dms", mirror.TZOffset))
		if tzoffset != 0 {
			hasTZAdjustement = true
		}

		s := MirrorStats{
			ID:        id,
			Name:      mirror.Name,
			Downloads: downloads,
			Bytes:     bytes,
			SyncOffset: SyncOffset{
				Valid:         !lastModTime.IsZero(),
				Value:         int(elapsed.Hours()),
				HumanReadable: utils.FuzzyTimeStr(elapsed),
			},
			TZOffset: tzoffset,
		}
		results = append(results, s)
		index += 2
	}

	sort.Sort(byDownloadNumbers{results})

	for i := 0; i < len(results); i++ {
		results[i].PercentD = float32(results[i].Downloads) * 100 / float32(maxdownloads)
		results[i].PercentB = float32(results[i].Bytes) * 100 / float32(maxbytes)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	err = ctx.Templates().mirrorstats.ExecuteTemplate(w, "base", MirrorStatsPage{results, mlist, GetConfig().LocalJSPath, hasTZAdjustement})
	if err != nil {
		log.Errorf("HTTP error: %s", err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}
