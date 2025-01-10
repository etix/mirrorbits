// Copyright (c) 2014-2019 Ludovic Fauvet
// Licensed under the MIT license

package daemon

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	. "github.com/etix/mirrorbits/config"
	"github.com/etix/mirrorbits/core"
	"github.com/etix/mirrorbits/database"
	"github.com/etix/mirrorbits/mirrors"
	"github.com/etix/mirrorbits/scan"
	"github.com/etix/mirrorbits/utils"
	"github.com/gomodule/redigo/redis"
	"github.com/op/go-logging"
	"github.com/pkg/errors"
)

var (
	healthCheckThreads  = 10
	userAgent           = "Mirrorbits/" + core.VERSION + " PING CHECK"
	clientTimeout       = time.Duration(20 * time.Second)
	clientDeadline      = time.Duration(40 * time.Second)
	errRedirect         = errors.New("Redirect not allowed")
	errMirrorNotScanned = errors.New("Mirror has not yet been scanned")

	log = logging.MustGetLogger("main")
)

type monitor struct {
	redis           *database.Redis
	cache           *mirrors.Cache
	mirrors         map[int]*mirror
	mapLock         sync.Mutex
	httpClient      http.Client
	httpTransport   http.Transport
	healthCheckChan chan int
	syncChan        chan int
	stop            chan struct{}
	configNotifier  chan bool
	wg              sync.WaitGroup
	formatLongestID int

	cluster *cluster
	trace   *scan.Trace
}

type mirror struct {
	mirrors.Mirror
	checking  bool
	scanning  bool
	lastCheck time.Time
}

func (m *mirror) NeedHealthCheck() bool {
	return time.Since(m.lastCheck) > time.Duration(GetConfig().CheckInterval)*time.Minute
}

func (m *mirror) NeedSync() bool {
	return time.Since(m.LastSync.Time) > time.Duration(GetConfig().ScanInterval)*time.Minute
}

func (m *mirror) IsScanning() bool {
	return m.scanning
}

func (m *mirror) IsChecking() bool {
	return m.checking
}

// NewMonitor returns a new instance of monitor
func NewMonitor(r *database.Redis, c *mirrors.Cache) *monitor {
	m := new(monitor)
	m.redis = r
	m.cache = c
	m.cluster = NewCluster(r)
	m.mirrors = make(map[int]*mirror)
	m.healthCheckChan = make(chan int, healthCheckThreads*5)
	m.syncChan = make(chan int)
	m.stop = make(chan struct{})
	m.configNotifier = make(chan bool, 1)
	m.trace = scan.NewTraceHandler(m.redis, m.stop)

	SubscribeConfig(m.configNotifier)

	rand.Seed(time.Now().UnixNano())

	m.httpTransport = http.Transport{
		DisableKeepAlives:   true,
		MaxIdleConnsPerHost: 0,
		Dial: func(network, addr string) (net.Conn, error) {
			deadline := time.Now().Add(clientDeadline)
			c, err := net.DialTimeout(network, addr, clientTimeout)
			if err != nil {
				return nil, err
			}
			c.SetDeadline(deadline)
			return c, nil
		},
	}

	m.httpClient = http.Client{
		CheckRedirect: checkRedirect,
		Transport:     &m.httpTransport,
	}
	return m
}

func (m *monitor) Stop() {
	select {
	case _, _ = <-m.stop:
		return
	default:
		m.cluster.Stop()
		close(m.stop)
	}
}

func (m *monitor) Wait() {
	m.wg.Wait()
}

// Return an error if the endpoint is an unauthorized redirect
func checkRedirect(req *http.Request, via []*http.Request) error {
	redirects := req.Context().Value(core.ContextAllowRedirects).(mirrors.Redirects)

	if redirects.Allowed() {
		return nil
	}

	name := req.Context().Value(core.ContextMirrorName)
	for _, r := range via {
		if r.URL != nil {
			log.Warningf("Unauthorized redirection for %s: %s => %s", name, r.URL.String(), req.URL.String())
		}
	}
	return errRedirect
}

// Main monitor loop
func (m *monitor) MonitorLoop() {
	m.wg.Add(1)
	defer m.wg.Done()

	mirrorUpdateEvent := m.cache.GetMirrorInvalidationEvent()

	// Wait until the database is ready to be used
	for {
		r := m.redis.Get()
		if r.Err() != nil {
			if _, ok := r.Err().(database.NetReadyError); ok {
				time.Sleep(100 * time.Millisecond)
				continue
			}
		}
		break
	}

	// Scan the local repository
	m.retry(func(i uint) error {
		err := m.scanRepository()
		if err != nil {
			if i == 0 {
				log.Errorf("%+v", errors.Wrap(err, "unable to scan the local repository"))
			}
			return err
		}
		return nil
	}, 1*time.Second)

	// Synchronize the list of all known mirrors
	m.retry(func(i uint) error {
		ids, err := m.mirrorsID()
		if err != nil {
			if i == 0 {
				log.Errorf("%+v", errors.Wrap(err, "unable to retrieve the mirror list"))
			}
			return err
		}
		err = m.syncMirrorList(ids...)
		if err != nil {
			if i == 0 {
				log.Errorf("%+v", errors.Wrap(err, "unable to sync the list of mirrors"))
			}
			return err
		}
		return nil
	}, 500*time.Millisecond)

	if utils.IsStopped(m.stop) {
		return
	}

	// Start the cluster manager
	m.cluster.Start()

	// Start the health check routines
	for i := 0; i < healthCheckThreads; i++ {
		m.wg.Add(1)
		go m.healthCheckLoop()
	}

	// Start the mirror sync routines
	for i := 0; i < GetConfig().ConcurrentSync; i++ {
		m.wg.Add(1)
		go m.syncLoop()
	}

	// Setup recurrent tasks
	var repositoryScanTicker <-chan time.Time
	repositoryScanInterval := -1
	mirrorCheckTicker := time.NewTicker(1 * time.Second)

	// Disable the mirror check while stopping to avoid spurious events
	go func() {
		select {
		case <-m.stop:
			mirrorCheckTicker.Stop()
		}
	}()

	// Force a first configuration reload to setup the timers
	select {
	case m.configNotifier <- true:
	default:
	}

	for {
		select {
		case <-m.stop:
			return
		case v := <-mirrorUpdateEvent:
			id, err := strconv.Atoi(v)
			if err == nil {
				m.syncMirrorList(id)
			}
		case <-m.configNotifier:
			if repositoryScanInterval != GetConfig().RepositoryScanInterval {
				repositoryScanInterval = GetConfig().RepositoryScanInterval

				if repositoryScanInterval == 0 {
					repositoryScanTicker = nil
				} else {
					repositoryScanTicker = time.Tick(time.Duration(repositoryScanInterval) * time.Minute)
				}
			}
		case <-repositoryScanTicker:
			m.scanRepository()
		case <-mirrorCheckTicker.C:
			if m.redis.Failure() {
				continue
			}
			m.mapLock.Lock()
			for id, v := range m.mirrors {
				if !v.Enabled {
					// Ignore disabled mirrors
					continue
				}
				if !m.cluster.IsHandled(id) {
					continue
				}
				if v.NeedHealthCheck() && !v.IsChecking() {
					select {
					case m.healthCheckChan <- id:
						m.mirrors[id].checking = true
					default:
					}
				}
				if v.NeedSync() && !v.IsScanning() {
					select {
					case m.syncChan <- id:
						m.mirrors[id].scanning = true
					default:
					}
				}
			}
			m.mapLock.Unlock()
		}
	}
}

// Returns a list of all mirrors ID
func (m *monitor) mirrorsID() ([]int, error) {
	var ids []int
	list, err := m.redis.GetListOfMirrors()
	if err != nil {
		return nil, err
	}
	for id := range list {
		ids = append(ids, id)
	}
	return ids, nil
}

// Sync the remote mirror struct with the local dataset
func (m *monitor) syncMirrorList(mirrorsIDs ...int) error {
	for _, id := range mirrorsIDs {
		mir, err := m.cache.GetMirror(id)
		if err != nil && err != redis.ErrNil {
			log.Errorf("Fetching mirror %s failed: %s", id, err.Error())
			continue
		} else if err == redis.ErrNil {
			// Mirror has been deleted
			m.mapLock.Lock()
			delete(m.mirrors, id)
			m.mapLock.Unlock()
			m.cluster.RemoveMirrorID(id)
			continue
		}

		// Compute the space required to display the mirror names in the logs
		if len(mir.Name) > m.formatLongestID {
			m.formatLongestID = len(mir.Name)
		}

		m.cluster.AddMirror(&mir)

		m.mapLock.Lock()
		if _, ok := m.mirrors[mir.ID]; ok {
			// Update existing mirror
			tmp := m.mirrors[mir.ID]
			tmp.Mirror = mir
			m.mirrors[mir.ID] = tmp
		} else {
			// Add new mirror
			m.mirrors[mir.ID] = &mirror{
				Mirror: mir,
			}
		}
		m.mapLock.Unlock()
	}

	log.Debugf("%d mirror%s updated", len(mirrorsIDs), utils.Plural(len(mirrorsIDs)))
	return nil
}

// Main health check loop
// TODO merge with the monitorLoop?
func (m *monitor) healthCheckLoop() {
	defer m.wg.Done()
	for {
		select {
		case <-m.stop:
			return
		case id := <-m.healthCheckChan:
			if utils.IsStopped(m.stop) {
				return
			}

			var mptr *mirror
			var mirror mirror
			var ok bool

			m.mapLock.Lock()
			if mptr, ok = m.mirrors[id]; !ok {
				m.mapLock.Unlock()
				continue
			}

			// Copy the mirror struct for read-only access
			mirror = *mptr
			m.mapLock.Unlock()

			err := m.healthCheck(mirror.Mirror)

			if err == errMirrorNotScanned {
				// Not removing the 'checking' lock is intended here so the mirror won't
				// be checked again until the rsync/ftp scan is finished.
				continue
			}

			m.mapLock.Lock()
			if mirror, ok := m.mirrors[id]; ok {
				if !database.RedisIsLoading(err) {
					mirror.lastCheck = time.Now().UTC()
				}
				mirror.checking = false
			}
			m.mapLock.Unlock()
		}
	}
}

// Main sync loop
// TODO merge with the monitorLoop?
func (m *monitor) syncLoop() {
	defer m.wg.Done()
	for {
		select {
		case <-m.stop:
			return
		case id := <-m.syncChan:

			var mir mirror
			var mirrorPtr *mirror
			var ok bool

			m.mapLock.Lock()
			if mirrorPtr, ok = m.mirrors[id]; !ok {
				m.mapLock.Unlock()
				continue
			}
			mir = *mirrorPtr
			m.mapLock.Unlock()

			conn := m.redis.Get()
			scanning, err := scan.IsScanning(conn, id)
			if err != nil {
				conn.Close()
				if !database.RedisIsLoading(err) {
					log.Warningf("syncloop: %s", err.Error())
				}
				goto end
			} else if scanning {
				log.Debugf("[%s] scan already in progress on another node", mir.Name)
				conn.Close()
				goto end
			}
			conn.Close()

			log.Debugf("Scanning %s", mir.Name)

			// Start fetching the latest trace
			go func() {
				err := m.trace.GetLastUpdate(mir.Mirror)
				if err != nil && err != scan.ErrNoTrace {
					if numError, ok := err.(*strconv.NumError); ok {
						if numError.Err == strconv.ErrSyntax {
							log.Warningf("[%s] parsing trace file failed: %s is not a valid timestamp", mir.Name, strconv.Quote(numError.Num))
							return
						}
					} else {
						log.Warningf("[%s] fetching trace file failed: %s", mir.Name, err)
					}
				}
			}()

			err = scan.ErrNoSyncMethod

			// First try to scan with rsync
			if mir.RsyncURL != "" {
				_, err = scan.Scan(core.RSYNC, m.redis, m.cache, mir.RsyncURL, id, m.stop)
			}
			// If it failed or rsync wasn't supported
			// fallback to FTP
			if err != nil && err != scan.ErrScanAborted && mir.FtpURL != "" {
				_, err = scan.Scan(core.FTP, m.redis, m.cache, mir.FtpURL, id, m.stop)
			}

			if err == scan.ErrScanInProgress {
				log.Warningf("%-30.30s Scan already in progress", mir.Name)
				goto end
			}

			if err == nil && mir.Enabled == true && mir.IsUp() == false {
				m.healthCheckChan <- id
			}

		end:
			m.mapLock.Lock()
			if mirrorPtr, ok = m.mirrors[id]; ok {
				mirrorPtr.scanning = false
			}
			m.mapLock.Unlock()
		}
	}
}

// Do an actual health check against a given mirror
func (m *monitor) healthCheck(mirror mirrors.Mirror) error {
	// Get the URL to a random file available on this mirror
	file, size, err := m.getRandomFile(mirror.ID)
	if err != nil {
		if err == redis.ErrNil {
			return errMirrorNotScanned
		} else if !database.RedisIsLoading(err) {
			log.Warningf("%s: Error: Cannot obtain a random file: %s", mirror.Name, err)
		}
		return err
	}

	// Perform health check
	err = m.healthCheckDo(&mirror, file, size)

	return err
}

func (m *monitor) healthCheckDo(mirror *mirrors.Mirror, file string, size int64) error {
	// Prepare url
	url := mirror.HttpURL

	// Get protocol
	proto := mirrors.HTTP
	if strings.HasPrefix(url, "https://") {
		proto = mirrors.HTTPS
	}

	// Format log output
	format := "%-" + fmt.Sprintf("%d.%ds %-5s ", m.formatLongestID+4, m.formatLongestID+4, proto)

	// Prepare the HTTP request
	req, err := http.NewRequest("HEAD", strings.TrimRight(url, "/")+file, nil)
	req.Header.Set("User-Agent", userAgent)
	req.Close = true

	ctx, cancel := context.WithTimeout(req.Context(), clientDeadline)
	ctx = context.WithValue(ctx, core.ContextMirrorID, mirror.ID)
	ctx = context.WithValue(ctx, core.ContextMirrorName, mirror.Name)
	ctx = context.WithValue(ctx, core.ContextAllowRedirects, mirror.AllowRedirects)
	req = req.WithContext(ctx)
	defer cancel()

	go func() {
		select {
		case <-m.stop:
			log.Debugf("Aborting health-check for %s", url)
			cancel()
		case <-ctx.Done():
		}
	}()

	var contentLength string
	var statusCode int
	elapsed, err := m.httpDo(ctx, req, func(resp *http.Response, err error) error {
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		statusCode = resp.StatusCode
		contentLength = resp.Header.Get("Content-Length")
		return nil
	})

	if utils.IsStopped(m.stop) {
		return nil
	}

	if err != nil {
		if opErr, ok := err.(*net.OpError); ok {
			log.Debugf("Op: %s | Net: %s | Addr: %s | Err: %s | Temporary: %t", opErr.Op, opErr.Net, opErr.Addr, opErr.Error(), opErr.Temporary())
		}
		reason := "Unreachable"
		if strings.Contains(err.Error(), errRedirect.Error()) {
			reason = "Unauthorized redirect"
		}
		markErr := mirrors.MarkMirrorDown(m.redis, mirror.ID, proto, reason)
		if markErr != nil {
			log.Errorf(format+"Unable to mark mirror as down: %s", mirror.Name, markErr)
		}
		log.Errorf(format+"Error: %s (%dms)", mirror.Name, err.Error(), elapsed/time.Millisecond)
		return err
	}

	switch statusCode {
	case 200:
		err = mirrors.MarkMirrorUp(m.redis, mirror.ID, proto)
		if err != nil {
			log.Errorf(format+"Unable to mark mirror as up: %s", mirror.Name, err)
		}
		rsize, err := strconv.ParseInt(contentLength, 10, 64)
		if err == nil && rsize != size {
			log.Warningf(format+"File size mismatch! [%s] (%dms)", mirror.Name, file, elapsed/time.Millisecond)
		} else {
			log.Noticef(format+"Up! (%dms)", mirror.Name, elapsed/time.Millisecond)
		}
	case 404:
		err = mirrors.MarkMirrorDown(m.redis, mirror.ID, proto, fmt.Sprintf("File not found %s (error 404)", file))
		if err != nil {
			log.Errorf(format+"Unable to mark mirror as down: %s", mirror.Name, err)
		}
		if GetConfig().DisableOnMissingFile {
			err = mirrors.DisableMirror(m.redis, mirror.ID)
			if err != nil {
				log.Errorf(format+"Unable to disable mirror: %s", mirror.Name, err)
			}
		}
		log.Errorf(format+"Error: File %s not found (error 404)", mirror.Name, file)
	default:
		err = mirrors.MarkMirrorDown(m.redis, mirror.ID, proto, fmt.Sprintf("Got status code %d", statusCode))
		if err != nil {
			log.Errorf(format+"Unable to mark mirror as down: %s", mirror.Name, err)
		}
		log.Warningf(format+"Down! Status: %d", mirror.Name, statusCode)
	}
	return nil
}

func (m *monitor) httpDo(ctx context.Context, req *http.Request, f func(*http.Response, error) error) (time.Duration, error) {
	var elapsed time.Duration
	c := make(chan error, 1)

	go func() {
		start := time.Now()
		err := f(m.httpClient.Do(req))
		elapsed = time.Since(start)
		c <- err
	}()

	select {
	case <-ctx.Done():
		m.httpTransport.CancelRequest(req)
		<-c // Wait for f to return.
		return elapsed, ctx.Err()
	case err := <-c:
		return elapsed, err
	}
}

// Get a random filename known to be served by the given mirror
func (m *monitor) getRandomFile(id int) (file string, size int64, err error) {
	sinterKey := fmt.Sprintf("HANDLEDFILES_%d", id)

	rconn := m.redis.Get()
	defer rconn.Close()

	file, err = redis.String(rconn.Do("SRANDMEMBER", sinterKey))
	if err != nil {
		return
	}

	size, err = redis.Int64(rconn.Do("HGET", fmt.Sprintf("FILE_%s", file), "size"))
	if err != nil {
		return
	}

	return
}

// Trigger a sync of the local repository
func (m *monitor) scanRepository() error {
	err := scan.ScanSource(m.redis, false, m.stop)
	if err != nil {
		log.Errorf("Scanning source failed: %s", err.Error())
	}
	return err
}

// Retry a function until no errors is returned while still allowing
// the process to be stopped.
func (m *monitor) retry(fn func(iteration uint) error, delay time.Duration) {
	var i uint
	for {
		err := fn(i)
		i++
		if err == nil {
			break
		}
		select {
		case <-m.stop:
			return
		case <-time.After(delay):
		}
	}
}
