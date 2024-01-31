// Copyright (c) 2014-2019 Ludovic Fauvet
// Licensed under the MIT license

package rpc

import (
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"

	. "github.com/etix/mirrorbits/config"
	"github.com/etix/mirrorbits/core"
	"github.com/etix/mirrorbits/database"
	"github.com/etix/mirrorbits/mirrors"
	"github.com/etix/mirrorbits/network"
	"github.com/etix/mirrorbits/scan"
	"github.com/etix/mirrorbits/utils"
	"github.com/golang/protobuf/ptypes"
	"github.com/golang/protobuf/ptypes/empty"
	"github.com/gomodule/redigo/redis"
	"github.com/pkg/errors"
	context "golang.org/x/net/context"
	grpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"gopkg.in/yaml.v3"
)

var (
	// ErrNameAlreadyTaken is returned when the request name is already taken by another mirror
	ErrNameAlreadyTaken = errors.New("name already taken")
)

// CLI object handles the server side RPC of the CLI
type CLI struct {
	listener net.Listener
	server   *grpc.Server
	sig      chan<- os.Signal
	redis    *database.Redis
	cache    *mirrors.Cache
}

func (c *CLI) Start() error {
	var err error
	c.listener, err = net.Listen("tcp", GetConfig().RPCListenAddress)
	if err != nil {
		return err
	}
	c.server = grpc.NewServer(
		grpc.UnaryInterceptor(UnaryInterceptor),
		grpc.StreamInterceptor(StreamInterceptor),
	)
	RegisterCLIServer(c.server, c)
	reflection.Register(c.server)
	go func() {
		if err := c.server.Serve(c.listener); err != nil {
			log.Fatalf("failed to serve rpc: %v", err)
		}
	}()
	return nil
}

func (c *CLI) Close() error {
	c.server.Stop()
	return c.listener.Close()
}

func (c *CLI) SetSignals(sig chan<- os.Signal) {
	c.sig = sig
}

func (c *CLI) SetDatabase(r *database.Redis) {
	c.redis = r
}

func (c *CLI) SetCache(cache *mirrors.Cache) {
	c.cache = cache
}

func (c *CLI) Ping(context.Context, *empty.Empty) (*empty.Empty, error) {
	return &empty.Empty{}, nil
}

func (c *CLI) GetVersion(context.Context, *empty.Empty) (*VersionReply, error) {
	return &VersionReply{
		Version:    core.VERSION,
		Build:      core.BUILD + core.DEV,
		GoVersion:  runtime.Version(),
		OS:         runtime.GOOS,
		Arch:       runtime.GOARCH,
		GoMaxProcs: int32(runtime.GOMAXPROCS(0)),
	}, nil
}

func (c *CLI) Upgrade(ctx context.Context, in *empty.Empty) (*empty.Empty, error) {
	select {
	case c.sig <- syscall.SIGUSR2:
	default:
		return nil, status.Error(codes.Internal, "signal handler not ready")
	}
	return &empty.Empty{}, nil
}

func (c *CLI) Reload(ctx context.Context, in *empty.Empty) (*empty.Empty, error) {
	select {
	case c.sig <- syscall.SIGHUP:
	default:
		return nil, status.Error(codes.Internal, "signal handler not ready")
	}
	return &empty.Empty{}, nil
}

func (c *CLI) MatchMirror(ctx context.Context, in *MatchRequest) (*MatchReply, error) {
	if c.redis == nil {
		return nil, status.Error(codes.Internal, "database not ready")
	}

	mirrors, err := c.redis.GetListOfMirrors()
	if err != nil {
		return nil, errors.Wrap(err, "can't fetch the list of mirrors")
	}

	reply := &MatchReply{}

	for id, name := range mirrors {
		if strings.Contains(strings.ToLower(name), strings.ToLower(in.Pattern)) {
			reply.Mirrors = append(reply.Mirrors, &MirrorID{
				ID:   int32(id),
				Name: name,
			})
		}
	}

	return reply, nil
}

func (c *CLI) ChangeStatus(ctx context.Context, in *ChangeStatusRequest) (*empty.Empty, error) {
	if in.ID <= 0 {
		return nil, status.Error(codes.FailedPrecondition, "invalid mirror id")
	}

	var err error

	switch in.Enabled {
	case true:
		err = mirrors.EnableMirror(c.redis, int(in.ID))
	case false:
		err = mirrors.DisableMirror(c.redis, int(in.ID))
	}

	return &empty.Empty{}, err
}

func (c *CLI) List(ctx context.Context, in *empty.Empty) (*MirrorListReply, error) {
	conn, err := c.redis.Connect()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	mirrorsIDs, err := c.redis.GetListOfMirrors()
	if err != nil {
		return nil, errors.Wrap(err, "can't fetch the list of mirrors")
	}

	conn.Send("MULTI")
	for id := range mirrorsIDs {
		conn.Send("HGETALL", fmt.Sprintf("MIRROR_%d", id))
	}

	res, err := redis.Values(conn.Do("EXEC"))
	if err != nil {
		return nil, errors.Wrap(err, "database error")
	}

	reply := &MirrorListReply{}

	for _, e := range res {
		var mirror mirrors.Mirror
		res, ok := e.([]interface{})
		if !ok {
			return nil, errors.New("typecast failed")
		}
		err = redis.ScanStruct([]interface{}(res), &mirror)
		if err != nil {
			return nil, errors.Wrap(err, "scan struct failed")
		}
		m, err := MirrorToRPC(&mirror)
		if err != nil {
			return nil, err
		}
		reply.Mirrors = append(reply.Mirrors, m)
	}

	return reply, nil
}

func (c *CLI) MirrorInfo(ctx context.Context, in *MirrorIDRequest) (*Mirror, error) {
	if in.ID <= 0 {
		return nil, status.Error(codes.FailedPrecondition, "invalid mirror id")
	}

	conn, err := c.redis.Connect()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	m, err := redis.Values(conn.Do("HGETALL", fmt.Sprintf("MIRROR_%d", in.ID)))
	if err != nil {
		return nil, err
	}

	var mi mirrors.Mirror
	err = redis.ScanStruct(m, &mi)
	if err != nil {
		return nil, err
	}

	rpcm, err := MirrorToRPC(&mi)
	if err != nil {
		return nil, err
	}

	return rpcm, nil
}

func (c *CLI) AddMirror(ctx context.Context, in *Mirror) (*AddMirrorReply, error) {
	mirror, err := MirrorFromRPC(in)
	if err != nil {
		return nil, err
	}

	if mirror.ID != 0 {
		return nil, status.Error(codes.FailedPrecondition, "unexpected ID")
	}

	u, err := url.Parse(mirror.HttpURL)
	if err != nil {
		return nil, errors.Wrap(err, "can't parse http url")
	}

	reply := &AddMirrorReply{}

	ip, err := network.LookupMirrorIP(u.Host)
	if err == network.ErrMultipleAddresses {
		reply.Warnings = append(reply.Warnings,
			"Warning: the hostname returned more than one address. Assuming they're sharing the same location.")
	} else if err != nil {
		return nil, errors.Wrap(err, "IP lookup failed")
	}

	geo := network.NewGeoIP()
	if err := geo.LoadGeoIP(); err != nil {
		return nil, errors.WithStack(err)
	}

	geoRec := geo.GetRecord(ip)
	if geoRec.IsValid() {
		mirror.Latitude = geoRec.Latitude
		mirror.Longitude = geoRec.Longitude
		mirror.ContinentCode = geoRec.ContinentCode
		mirror.CountryCodes = geoRec.CountryCode
		mirror.Asnum = geoRec.ASNum

		reply.Latitude = geoRec.Latitude
		reply.Longitude = geoRec.Longitude
		reply.Continent = geoRec.ContinentCode
		reply.Country = geoRec.Country
		reply.ASN = fmt.Sprintf("%s (%d)", geoRec.ASName, geoRec.ASNum)
	} else {
		reply.Warnings = append(reply.Warnings,
			"Warning: unable to guess the geographic location of this mirror")
	}

	return reply, c.setMirror(mirror)
}

func (c *CLI) UpdateMirror(ctx context.Context, in *Mirror) (*UpdateMirrorReply, error) {
	mirror, err := MirrorFromRPC(in)
	if err != nil {
		return nil, err
	}

	if mirror.ID <= 0 {
		return nil, status.Error(codes.FailedPrecondition, "invalid mirror id")
	}

	conn, err := c.redis.Connect()
	if err != nil {
		return &UpdateMirrorReply{}, err
	}
	defer conn.Close()

	m, err := redis.Values(conn.Do("HGETALL", fmt.Sprintf("MIRROR_%d", mirror.ID)))
	if err != nil {
		return nil, err
	}

	var original mirrors.Mirror
	err = redis.ScanStruct(m, &original)
	if err != nil {
		return nil, err
	}

	diff := createDiff(&original, mirror)

	return &UpdateMirrorReply{
		Diff: diff,
	}, c.setMirror(mirror)
}

func createDiff(mirror1, mirror2 *mirrors.Mirror) (out string) {
	yamlo, _ := yaml.Marshal(mirror1)
	yamln, _ := yaml.Marshal(mirror2)

	splito := strings.Split(string(yamlo), "\n")
	splitn := strings.Split(string(yamln), "\n")

	for i, l := range splito {
		if l != splitn[i] {
			out += fmt.Sprintf("- %s\n+ %s\n", l, splitn[i])
		}
	}

	return
}

func (c *CLI) setMirror(mirror *mirrors.Mirror) error {
	conn, err := c.redis.Connect()
	if err != nil {
		return err
	}
	defer conn.Close()

	mirrorsIDs, err := c.redis.GetListOfMirrors()
	if err != nil {
		return errors.Wrap(err, "can't fetch the list of mirrors")
	}

	isUpdate := false

	for id, name := range mirrorsIDs {
		if id == mirror.ID {
			isUpdate = true
		}
		if mirror.ID != id && name == mirror.Name {
			return ErrNameAlreadyTaken
		}
	}

	if mirror.ID <= 0 {
		// Generate a new ID
		mirror.ID, err = redis.Int(conn.Do("INCR", "LAST_MID"))
		if err != nil {
			return errors.Wrap(err, "failed creating a new id")
		}
	}

	// Reformat contry codes
	mirror.CountryCodes = utils.SanitizeLocationCodes(mirror.CountryCodes)
	mirror.ExcludedCountryCodes = utils.SanitizeLocationCodes(mirror.ExcludedCountryCodes)

	// Reformat continent code
	mirror.ContinentCode = utils.SanitizeLocationCodes(mirror.ContinentCode)

	// Normalize URLs
	if mirror.HttpURL != "" {
		mirror.HttpURL = utils.NormalizeURL(mirror.HttpURL)
	}
	if mirror.RsyncURL != "" {
		mirror.RsyncURL = utils.NormalizeURL(mirror.RsyncURL)
	}
	if mirror.FtpURL != "" {
		mirror.FtpURL = utils.NormalizeURL(mirror.FtpURL)
	}

	// Save the values back into redis
	conn.Send("MULTI")
	conn.Send("HMSET", fmt.Sprintf("MIRROR_%d", mirror.ID),
		"ID", mirror.ID,
		"name", mirror.Name,
		"http", mirror.HttpURL,
		"rsync", mirror.RsyncURL,
		"ftp", mirror.FtpURL,
		"sponsorName", mirror.SponsorName,
		"sponsorURL", mirror.SponsorURL,
		"sponsorLogo", mirror.SponsorLogoURL,
		"adminName", mirror.AdminName,
		"adminEmail", mirror.AdminEmail,
		"customData", mirror.CustomData,
		"continentOnly", mirror.ContinentOnly,
		"countryOnly", mirror.CountryOnly,
		"asOnly", mirror.ASOnly,
		"score", mirror.Score,
		"latitude", mirror.Latitude,
		"longitude", mirror.Longitude,
		"continentCode", mirror.ContinentCode,
		"countryCodes", mirror.CountryCodes,
		"excludedCountryCodes", mirror.ExcludedCountryCodes,
		"asnum", mirror.Asnum,
		"comment", mirror.Comment,
		"allowredirects", mirror.AllowRedirects,
		"enabled", mirror.Enabled)

	// The name of the mirror has been changed.
	conn.Send("HSET", "MIRRORS", mirror.ID, mirror.Name)

	_, err = conn.Do("EXEC")
	if err != nil {
		return errors.Wrap(err, "couldn't save the mirror configuration")
	}

	// Publish update
	database.Publish(conn, database.MIRROR_UPDATE, strconv.Itoa(mirror.ID))

	if isUpdate {
		// This was an update of an existing mirror
		mirrors.PushLog(c.redis, mirrors.NewLogEdited(mirror.ID))
	} else {
		// We just added a new mirror
		mirrors.PushLog(c.redis, mirrors.NewLogAdded(mirror.ID))
	}

	return nil
}

func (c *CLI) RemoveMirror(ctx context.Context, in *MirrorIDRequest) (*empty.Empty, error) {
	if in.ID <= 0 {
		return nil, status.Error(codes.FailedPrecondition, "invalid mirror id")
	}

	conn, err := c.redis.Connect()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	// First disable the mirror
	err = mirrors.DisableMirror(c.redis, int(in.ID))
	if err != nil {
		return nil, errors.Wrap(err, "unable to disable the mirror")
	}

	// Get all files supported by the given mirror
	files, err := redis.Strings(conn.Do("SMEMBERS", fmt.Sprintf("MIRRORFILES_%d", in.ID)))
	if err != nil {
		return nil, errors.Wrap(err, "unable to fetch the file list")
	}

	conn.Send("MULTI")

	// Remove each FILEINFO / FILEMIRRORS
	for _, file := range files {
		conn.Send("DEL", fmt.Sprintf("FILEINFO_%d_%s", in.ID, file))
		conn.Send("SREM", fmt.Sprintf("FILEMIRRORS_%s", file), in.ID)
		conn.Send("PUBLISH", database.MIRROR_FILE_UPDATE, fmt.Sprintf("%d %s", in.ID, file))
	}

	// Remove all other keys
	conn.Send("DEL",
		fmt.Sprintf("MIRROR_%d", in.ID),
		fmt.Sprintf("MIRRORFILES_%d", in.ID),
		fmt.Sprintf("MIRRORFILESTMP_%d", in.ID),
		fmt.Sprintf("HANDLEDFILES_%d", in.ID),
		fmt.Sprintf("SCANNING_%d", in.ID),
		fmt.Sprintf("MIRRORLOGS_%d", in.ID))

	// Remove the last reference
	conn.Send("HDEL", "MIRRORS", in.ID)

	_, err = conn.Do("EXEC")
	if err != nil {
		return nil, errors.Wrap(err, "operation failed")
	}

	// Publish update
	database.Publish(conn, database.MIRROR_UPDATE, strconv.Itoa(int(in.ID)))

	return &empty.Empty{}, nil
}

func (c *CLI) RefreshRepository(ctx context.Context, in *RefreshRepositoryRequest) (*empty.Empty, error) {
	return &empty.Empty{}, scan.ScanSource(c.redis, in.Rehash, nil)
}

func (c *CLI) ScanMirror(ctx context.Context, in *ScanMirrorRequest) (*ScanMirrorReply, error) {
	if in.ID <= 0 {
		return nil, status.Error(codes.FailedPrecondition, "invalid mirror id")
	}

	conn, err := c.redis.Connect()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	// Check if the local repository has been scanned already
	exists, err := redis.Bool(conn.Do("EXISTS", "FILES"))
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, status.Error(codes.FailedPrecondition, "local repository not yet indexed. You should run 'refresh' first!")
	}

	key := fmt.Sprintf("MIRROR_%d", in.ID)
	m, err := redis.Values(conn.Do("HGETALL", key))
	if err != nil {
		return nil, err
	}

	var mirror mirrors.Mirror
	err = redis.ScanStruct(m, &mirror)
	if err != nil {
		return nil, err
	}

	var wg sync.WaitGroup
	trace := scan.NewTraceHandler(c.redis, make(<-chan struct{}))

	wg.Add(1)
	go func() {
		defer wg.Done()
		err := trace.GetLastUpdate(mirror)
		if err != nil && err != scan.ErrNoTrace {
			if numError, ok := err.(*strconv.NumError); ok {
				if numError.Err == strconv.ErrSyntax {
					//log.Warningf("[%s] parsing trace file failed: %s is not a valid timestamp", mirror.Name, strconv.Quote(numError.Num))
					return
				}
			} else {
				//log.Warningf("[%s] fetching trace file failed: %s", mirror.Name, err)
			}
		}
	}()

	err = scan.ErrNoSyncMethod
	var res *scan.ScanResult

	if in.Protocol == ScanMirrorRequest_ALL {
		// Use rsync (if applicable) and fallback to FTP
		if mirror.RsyncURL != "" {
			res, err = scan.Scan(core.RSYNC, c.redis, c.cache, mirror.RsyncURL, mirror.ID, ctx.Done())
		}
		if err != nil && mirror.FtpURL != "" {
			res, err = scan.Scan(core.FTP, c.redis, c.cache, mirror.FtpURL, mirror.ID, ctx.Done())
		}
	} else {
		// Use the requested protocol
		if in.Protocol == ScanMirrorRequest_RSYNC && mirror.RsyncURL != "" {
			res, err = scan.Scan(core.RSYNC, c.redis, c.cache, mirror.RsyncURL, mirror.ID, ctx.Done())
		} else if in.Protocol == ScanMirrorRequest_FTP && mirror.FtpURL != "" {
			res, err = scan.Scan(core.FTP, c.redis, c.cache, mirror.FtpURL, mirror.ID, ctx.Done())
		}
	}

	if err != nil {
		return nil, errors.New(fmt.Sprintf("scanning %s failed: %s", mirror.Name, err))
	}

	reply := &ScanMirrorReply{
		FilesIndexed: res.FilesIndexed,
		KnownIndexed: res.KnownIndexed,
		Removed:      res.Removed,
		TZOffsetMs:   res.TZOffsetMs,
	}

	// Finally enable the mirror if requested
	if err == nil && in.AutoEnable == true {
		if err := mirrors.EnableMirror(c.redis, mirror.ID); err != nil {
			return nil, errors.Wrap(err, "couldn't enable the mirror")
		}
		reply.Enabled = true
	}

	wg.Wait()

	return reply, nil
}

func (c *CLI) StatsFile(ctx context.Context, in *StatsFileRequest) (*StatsFileReply, error) {
	conn, err := c.redis.Connect()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	// Convert the timestamps
	start, err := ptypes.Timestamp(in.DateStart)
	if err != nil {
		return nil, err
	}
	end, err := ptypes.Timestamp(in.DateEnd)
	if err != nil {
		return nil, err
	}

	// Compile the regex pattern
	re, err := regexp.Compile(in.Pattern)
	if err != nil {
		return nil, err
	}

	// Generate the list of redis key for the period
	tkcoverage := utils.TimeKeyCoverage(start, end)

	// Prepare the transaction
	conn.Send("MULTI")

	for _, k := range tkcoverage {
		conn.Send("HGETALL", "STATS_FILE_"+k)
	}

	stats, err := redis.Values(conn.Do("EXEC"))
	if err != nil {
		return nil, errors.Wrap(err, "can't fetch stats")
	}

	reply := &StatsFileReply{
		Files: make(map[string]int64),
	}

	for _, res := range stats {
		line, ok := res.([]interface{})
		if !ok {
			return nil, errors.Wrap(err, "typecast failed")
		} else {
			stats := []interface{}(line)
			for i := 0; i < len(stats); i += 2 {
				path, _ := redis.String(stats[i], nil)
				matched := re.MatchString(path)
				if matched {
					reqs, _ := redis.Int64(stats[i+1], nil)
					reply.Files[path] += reqs
				}
			}
		}
	}

	return reply, nil
}

func (c *CLI) StatsMirror(ctx context.Context, in *StatsMirrorRequest) (*StatsMirrorReply, error) {
	if in.ID <= 0 {
		return nil, status.Error(codes.FailedPrecondition, "invalid mirror id")
	}

	conn, err := c.redis.Connect()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	// Convert the timestamps
	start, err := ptypes.Timestamp(in.DateStart)
	if err != nil {
		return nil, err
	}
	end, err := ptypes.Timestamp(in.DateEnd)
	if err != nil {
		return nil, err
	}

	// Generate the list of redis key for the period
	tkcoverage := utils.TimeKeyCoverage(start, end)

	conn.Send("MULTI")

	// Fetch the stats
	for _, k := range tkcoverage {
		conn.Send("HGET", "STATS_MIRROR_"+k, in.ID)
		conn.Send("HGET", "STATS_MIRROR_BYTES_"+k, in.ID)
	}

	stats, err := redis.Strings(conn.Do("EXEC"))
	if err != nil {
		return nil, errors.Wrap(err, "can't fetch stats")
	}

	// Fetch the mirror struct
	m, err := redis.Values(conn.Do("HGETALL", fmt.Sprintf("MIRROR_%d", in.ID)))
	if err != nil {
		return nil, errors.WithMessage(err, "can't fetch mirror")
	}

	reply := &StatsMirrorReply{}

	var mirror mirrors.Mirror
	err = redis.ScanStruct(m, &mirror)
	if err != nil {
		return nil, errors.Wrap(err, "stats error")
	}

	reply.Mirror, err = MirrorToRPC(&mirror)
	if err != nil {
		return nil, errors.Wrap(err, "stats error")
	}

	for i := 0; i < len(stats); i += 2 {
		v1, _ := strconv.ParseInt(stats[i], 10, 64)
		v2, _ := strconv.ParseInt(stats[i+1], 10, 64)
		reply.Requests += v1
		reply.Bytes += v2
	}

	return reply, nil
}

func (c *CLI) GetMirrorLogs(ctx context.Context, in *GetMirrorLogsRequest) (*GetMirrorLogsReply, error) {
	if in.ID <= 0 {
		return nil, status.Error(codes.FailedPrecondition, "invalid mirror id")
	}

	lines, err := mirrors.ReadLogs(c.redis, int(in.ID), int(in.MaxResults))
	if err != nil {
		return nil, errors.Wrap(err, "mirror logs error")
	}

	return &GetMirrorLogsReply{Line: lines}, nil
}
