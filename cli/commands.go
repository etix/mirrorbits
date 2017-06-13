// Copyright (c) 2014-2017 Ludovic Fauvet
// Licensed under the MIT license

package cli

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/etix/mirrorbits/core"
	"github.com/etix/mirrorbits/database"
	"github.com/etix/mirrorbits/filesystem"
	"github.com/etix/mirrorbits/mirrors"
	"github.com/etix/mirrorbits/network"
	"github.com/etix/mirrorbits/process"
	"github.com/etix/mirrorbits/scan"
	"github.com/etix/mirrorbits/utils"
	"github.com/garyburd/redigo/redis"
	"github.com/op/go-logging"
	"gopkg.in/yaml.v2"
)

const (
	commentSeparator = "##### Comments go below this line #####"
)

var (
	log = logging.MustGetLogger("main")

	// ErrNoSyncMethod is returned when no sync protocol is available
	ErrNoSyncMethod = errors.New("no suitable URL for the scan")
)

type cli struct{}

// ParseCommands parses the command line and call the appropriate functions
func ParseCommands(args ...string) error {
	c := &cli{}

	if len(args) > 0 && args[0] != "help" {
		method, exists := c.getMethod(args[0])
		if !exists {
			fmt.Println("Error: Command not found:", args[0])
			return c.CmdHelp()
		}
		ret := method.Func.CallSlice([]reflect.Value{
			reflect.ValueOf(c),
			reflect.ValueOf(args[1:]),
		})[0].Interface()
		if ret == nil {
			return nil
		}
		return ret.(error)
	}
	return c.CmdHelp()
}

func (c *cli) getMethod(name string) (reflect.Method, bool) {
	methodName := "Cmd" + strings.ToUpper(name[:1]) + strings.ToLower(name[1:])
	return reflect.TypeOf(c).MethodByName(methodName)
}

func (c *cli) CmdHelp() error {
	help := fmt.Sprintf("Usage: mirrorbits [OPTIONS] COMMAND [arg...]\n\nA smart download redirector.\n\nCommands:\n")
	for _, command := range [][]string{
		{"add", "Add a new mirror"},
		{"disable", "Disable a mirror"},
		{"edit", "Edit a mirror"},
		{"enable", "Enable a mirror"},
		{"export", "Export the mirror database"},
		{"list", "List all mirrors"},
		{"refresh", "Refresh the local repository"},
		{"reload", "Reload configuration"},
		{"remove", "Remove a mirror"},
		{"scan", "(Re-)Scan a mirror"},
		{"show", "Print a mirror configuration"},
		{"stats", "Show download stats"},
		{"upgrade", "Seamless binary upgrade"},
		{"version", "Print version information"},
	} {
		help += fmt.Sprintf("    %-10.10s%s\n", command[0], command[1])
	}
	fmt.Fprintf(os.Stderr, "%s\n", help)
	return nil
}

// SubCmd prints the usage of a subcommand
func SubCmd(name, signature, description string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.Usage = func() {
		fmt.Fprintf(os.Stderr, "\nUsage: mirrorbits %s %s\n\n%s\n\n", name, signature, description)
		flags.PrintDefaults()
	}
	return flags
}

func (c *cli) CmdList(args ...string) error {
	cmd := SubCmd("list", "", "Get the list of mirrors")
	http := cmd.Bool("http", false, "Print HTTP addresses")
	rsync := cmd.Bool("rsync", false, "Print rsync addresses")
	ftp := cmd.Bool("ftp", false, "Print FTP addresses")
	location := cmd.Bool("location", false, "Print the country and continent code")
	state := cmd.Bool("state", true, "Print the state of the mirror")
	score := cmd.Bool("score", false, "Print the score of the mirror")
	disabled := cmd.Bool("disabled", false, "List disabled mirrors only")
	enabled := cmd.Bool("enabled", false, "List enabled mirrors only")
	down := cmd.Bool("down", false, "List only mirrors currently down")

	if err := cmd.Parse(args); err != nil {
		return nil
	}
	if cmd.NArg() != 0 {
		cmd.Usage()
		return nil
	}

	r := database.NewRedis()
	conn, err := r.Connect()
	if err != nil {
		log.Fatal("Redis: ", err)
	}
	defer conn.Close()

	mirrorsIDs, err := redis.Strings(conn.Do("LRANGE", "MIRRORS", "0", "-1"))
	if err != nil {
		log.Fatal("Cannot fetch the list of mirrors: ", err)
	}

	conn.Send("MULTI")

	for _, e := range mirrorsIDs {
		conn.Send("HGETALL", fmt.Sprintf("MIRROR_%s", e))
	}

	res, err := redis.Values(conn.Do("EXEC"))
	if err != nil {
		log.Fatal("Redis: ", err)
	}

	w := new(tabwriter.Writer)
	w.Init(os.Stdout, 0, 8, 0, '\t', 0)
	fmt.Fprint(w, "Identifier ")
	if *score == true {
		fmt.Fprint(w, "\tSCORE")
	}
	if *http == true {
		fmt.Fprint(w, "\tHTTP ")
	}
	if *rsync == true {
		fmt.Fprint(w, "\tRSYNC ")
	}
	if *ftp == true {
		fmt.Fprint(w, "\tFTP ")
	}
	if *location == true {
		fmt.Fprint(w, "\tLOCATION ")
	}
	if *state == true {
		fmt.Fprint(w, "\tSTATE\tSINCE")
	}
	fmt.Fprint(w, "\n")

	for _, e := range res {
		var mirror mirrors.Mirror
		res, ok := e.([]interface{})
		if !ok {
			log.Fatal("Typecast failed")
		} else {
			err := redis.ScanStruct([]interface{}(res), &mirror)
			if err != nil {
				log.Fatal("ScanStruct:", err)
			}
			if *disabled == true {
				if mirror.Enabled == true {
					continue
				}
			}
			if *enabled == true {
				if mirror.Enabled == false {
					continue
				}
			}
			if *down == true {
				if mirror.Up == true {
					continue
				}
			}
			fmt.Fprintf(w, "%s ", mirror.ID)
			if *score == true {
				fmt.Fprintf(w, "\t%d ", mirror.Score)
			}
			if *http == true {
				fmt.Fprintf(w, "\t%s ", mirror.HttpURL)
			}
			if *rsync == true {
				fmt.Fprintf(w, "\t%s ", mirror.RsyncURL)
			}
			if *ftp == true {
				fmt.Fprintf(w, "\t%s ", mirror.FtpURL)
			}
			if *location == true {
				countries := strings.Split(mirror.CountryCodes, " ")
				countryCode := "/"
				if len(countries) >= 1 {
					countryCode = countries[0]
				}
				fmt.Fprintf(w, "\t%s (%s) ", countryCode, mirror.ContinentCode)
			}
			if *state == true {
				if mirror.Enabled == false {
					fmt.Fprintf(w, "\tdisabled")
				} else if mirror.Up == true {
					fmt.Fprintf(w, "\tup")
				} else {
					fmt.Fprintf(w, "\tdown")
				}
				fmt.Fprintf(w, " \t(%s)", mirror.StateSince.Format(time.RFC1123))
			}
			fmt.Fprint(w, "\n")
		}
	}

	w.Flush()

	return nil
}

func (c *cli) CmdAdd(args ...string) error {
	cmd := SubCmd("add", "[OPTIONS] IDENTIFIER", "Add a new mirror")
	http := cmd.String("http", "", "HTTP base URL")
	rsync := cmd.String("rsync", "", "RSYNC base URL (for scanning only)")
	ftp := cmd.String("ftp", "", "FTP base URL (for scanning only)")
	sponsorName := cmd.String("sponsor-name", "", "Name of the sponsor")
	sponsorURL := cmd.String("sponsor-url", "", "URL of the sponsor")
	sponsorLogo := cmd.String("sponsor-logo", "", "URL of a logo to display for this mirror")
	adminName := cmd.String("admin-name", "", "Admin's name")
	adminEmail := cmd.String("admin-email", "", "Admin's email")
	customData := cmd.String("custom-data", "", "Associated data to return when the mirror is selected (i.e. json document)")
	continentOnly := cmd.Bool("continent-only", false, "The mirror should only handle its continent")
	countryOnly := cmd.Bool("country-only", false, "The mirror should only handle its country")
	asOnly := cmd.Bool("as-only", false, "The mirror should only handle clients in the same AS number")
	score := cmd.Int("score", 0, "Weight to give to the mirror during selection")
	comment := cmd.String("comment", "", "Comment")

	if err := cmd.Parse(args); err != nil {
		return nil
	}
	if cmd.NArg() < 1 {
		cmd.Usage()
		return nil
	}

	if strings.Contains(cmd.Arg(0), " ") {
		fmt.Fprintf(os.Stderr, "The identifier cannot contain a space\n")
		os.Exit(-1)
	}

	if *http == "" {
		fmt.Fprintf(os.Stderr, "You *must* pass at least an HTTP URL\n")
		os.Exit(-1)
	}

	if !strings.HasPrefix(*http, "http://") && !strings.HasPrefix(*http, "https://") {
		*http = "http://" + *http
	}

	u, err := url.Parse(*http)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Can't parse HTTP url\n")
		os.Exit(-1)
	}

	ip, err := network.LookupMirrorIP(u.Host)
	if err == network.ErrMultipleAddresses {
		fmt.Fprintf(os.Stderr, "Warning: the hostname returned more than one address! This is highly unreliable.\n")
	} else if err != nil {
		log.Fatal("IP lookup failed: ", err.Error())
	}

	geo := network.NewGeoIP()
	if err := geo.LoadGeoIP(); err != nil {
		log.Fatal(err.Error())
	}

	geoRec := geo.GetRecord(ip)

	r := database.NewRedis()
	conn, err := r.Connect()
	if err != nil {
		log.Fatal("Redis: ", err)
	}
	defer conn.Close()

	key := fmt.Sprintf("MIRROR_%s", cmd.Arg(0))
	exists, err := redis.Bool(conn.Do("EXISTS", key))
	if err != nil {
		return err
	}
	if exists {
		fmt.Fprintf(os.Stderr, "Mirror %s already exists!\n", cmd.Arg(0))
		os.Exit(-1)
	}

	// Normalize the URLs
	if http != nil {
		*http = utils.NormalizeURL(*http)
	}
	if rsync != nil {
		*rsync = utils.NormalizeURL(*rsync)
	}
	if ftp != nil {
		*ftp = utils.NormalizeURL(*ftp)
	}

	var latitude, longitude float32
	var continentCode, countryCode string

	if geoRec.IsValid() {
		latitude = geoRec.Latitude
		longitude = geoRec.Longitude
		continentCode = geoRec.ContinentCode
		countryCode = geoRec.CountryCode
	} else {
		fmt.Fprintf(os.Stderr, "Warning: unable to guess the geographic location of %s\n", cmd.Arg(0))
	}

	_, err = conn.Do("HMSET", key,
		"ID", cmd.Arg(0),
		"http", *http,
		"rsync", *rsync,
		"ftp", *ftp,
		"sponsorName", *sponsorName,
		"sponsorURL", *sponsorURL,
		"sponsorLogo", *sponsorLogo,
		"adminName", *adminName,
		"adminEmail", *adminEmail,
		"customData", *customData,
		"continentOnly", *continentOnly,
		"countryOnly", *countryOnly,
		"asOnly", *asOnly,
		"score", *score,
		"latitude", fmt.Sprintf("%f", latitude),
		"longitude", fmt.Sprintf("%f", longitude),
		"continentCode", continentCode,
		"countryCodes", countryCode,
		"asnum", geoRec.ASNum,
		"comment", strings.TrimSpace(*comment),
		"enabled", false,
		"up", false)
	if err != nil {
		goto oops
	}

	_, err = conn.Do("LPUSH", "MIRRORS", cmd.Arg(0))
	if err != nil {
		goto oops
	}

	// Publish update
	database.Publish(conn, database.MIRROR_UPDATE, cmd.Arg(0))

	fmt.Println("Mirror added successfully")
	return nil
oops:
	fmt.Fprintf(os.Stderr, "Oops: %s", err)
	os.Exit(-1)
	return nil
}

func (c *cli) CmdRemove(args ...string) error {
	cmd := SubCmd("remove", "IDENTIFIER", "Remove an existing mirror")
	force := cmd.Bool("f", false, "Never prompt for confirmation")

	if err := cmd.Parse(args); err != nil {
		return nil
	}
	if cmd.NArg() != 1 {
		cmd.Usage()
		return nil
	}

	// Guess which mirror to use
	list, err := c.matchMirror(cmd.Arg(0))
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Fprintf(os.Stderr, "No match for %s\n", cmd.Arg(0))
		return nil
	} else if len(list) > 1 {
		for _, e := range list {
			fmt.Fprintf(os.Stderr, "%s\n", e)
		}
		return nil
	}

	identifier := list[0]

	if *force == false {
		fmt.Printf("Removing %s, are you sure? [y/N]", identifier)
		reader := bufio.NewReader(os.Stdin)
		s, _ := reader.ReadString('\n')
		switch s[0] {
		case 'y', 'Y':
			break
		default:
			fmt.Println("Skipped")
			return nil
		}
	}

	r := database.NewRedis()
	conn, err := r.Connect()
	if err != nil {
		log.Fatal("Redis: ", err)
	}
	defer conn.Close()

	// First disable the mirror
	mirrors.DisableMirror(r, identifier)

	// Get all files supported by the given mirror
	files, err := redis.Strings(conn.Do("SMEMBERS", fmt.Sprintf("MIRROR_%s_FILES", identifier)))
	if err != nil {
		log.Fatal("Error: Cannot fetch file list: ", err)
	}

	conn.Send("MULTI")

	// Remove each FILEINFO / FILEMIRRORS
	for _, file := range files {
		conn.Send("DEL", fmt.Sprintf("FILEINFO_%s_%s", identifier, file))
		conn.Send("SREM", fmt.Sprintf("FILEMIRRORS_%s", file), identifier)
		conn.Send("PUBLISH", database.MIRROR_FILE_UPDATE, fmt.Sprintf("%s %s", identifier, file))
	}

	_, err = conn.Do("EXEC")
	if err != nil {
		log.Fatal("Error: FILEINFO/FILEMIRRORS keys could not be removed: ", err)
	}

	// Remove all other keys
	_, err = conn.Do("DEL",
		fmt.Sprintf("MIRROR_%s", identifier),
		fmt.Sprintf("MIRROR_%s_FILES", identifier),
		fmt.Sprintf("MIRROR_%s_FILES_TMP", identifier),
		fmt.Sprintf("HANDLEDFILES_%s", identifier),
		fmt.Sprintf("SCANNING_%s", identifier))

	if err != nil {
		log.Fatal("Error: MIRROR keys could not be removed: ", err)
	}

	// Remove the last reference
	_, err = conn.Do("LREM", "MIRRORS", 0, identifier)

	if err != nil {
		log.Fatal("Error: Could not remove the reference from key MIRRORS")
	}

	// Publish update
	database.Publish(conn, database.MIRROR_UPDATE, identifier)

	fmt.Println("Mirror removed successfully")
	return nil
}

func (c *cli) CmdScan(args ...string) error {
	cmd := SubCmd("scan", "[IDENTIFIER]", "(Re-)Scan a mirror")
	enable := cmd.Bool("enable", false, "Enable the mirror automatically if the scan is successful")
	all := cmd.Bool("all", false, "Scan all mirrors at once")
	ftp := cmd.Bool("ftp", false, "Force a scan using FTP")
	rsync := cmd.Bool("rsync", false, "Force a scan using rsync")

	if err := cmd.Parse(args); err != nil {
		return nil
	}
	if !*all && cmd.NArg() != 1 || *all && cmd.NArg() != 0 {
		cmd.Usage()
		return nil
	}

	r := database.NewRedis()
	conn, err := r.Connect()
	if err != nil {
		log.Fatal("Redis: ", err)
	}
	defer conn.Close()

	// Check if the local repository has been scanned already
	exists, err := redis.Bool(conn.Do("EXISTS", "FILES"))
	if err != nil {
		return err
	}
	if !exists {
		fmt.Fprintf(os.Stderr, "Local repository not yet indexed.\nYou should run 'refresh' first!\n")
		os.Exit(-1)
	}

	var list []string

	if *all == true {
		list, err = redis.Strings(conn.Do("LRANGE", "MIRRORS", "0", "-1"))
		if err != nil {
			return errors.New("Cannot fetch the list of mirrors")
		}
	} else {
		list, err = c.matchMirror(cmd.Arg(0))
		if err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Fprintf(os.Stderr, "No match for %s\n", cmd.Arg(0))
			return nil
		} else if len(list) > 1 {
			for _, e := range list {
				fmt.Fprintf(os.Stderr, "%s\n", e)
			}
			return nil
		}
	}

	var wg sync.WaitGroup
	stop := make(chan bool)
	trace := scan.NewTraceHandler(r, stop)

	for _, id := range list {

		key := fmt.Sprintf("MIRROR_%s", id)
		m, err := redis.Values(conn.Do("HGETALL", key))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Cannot fetch mirror details: %s\n", err)
			return err
		}

		var mirror mirrors.Mirror
		err = redis.ScanStruct(m, &mirror)
		if err != nil {
			return err
		}

		log.Noticef("Scanning %s...", id)

		wg.Add(1)
		go func() {
			defer wg.Done()
			err := trace.GetLastUpdate(mirror)
			if err != nil && err != scan.ErrNoTrace {
				if numError, ok := err.(*strconv.NumError); ok {
					if numError.Err == strconv.ErrSyntax {
						log.Warningf("[%s] parsing trace file failed: %s is not a valid timestamp", mirror.ID, strconv.Quote(numError.Num))
						return
					}
				} else {
					log.Warningf("[%s] fetching trace file failed: %s", mirror.ID, err)
				}
			}
		}()

		err = ErrNoSyncMethod

		if *rsync == true || *ftp == true {
			// Use the requested protocol
			if *rsync == true && mirror.RsyncURL != "" {
				err = scan.Scan(scan.RSYNC, r, mirror.RsyncURL, id, nil)
			} else if *ftp == true && mirror.FtpURL != "" {
				err = scan.Scan(scan.FTP, r, mirror.FtpURL, id, nil)
			}
		} else {
			// Use rsync (if applicable) and fallback to FTP
			if mirror.RsyncURL != "" {
				err = scan.Scan(scan.RSYNC, r, mirror.RsyncURL, id, nil)
			}
			if err != nil && mirror.FtpURL != "" {
				err = scan.Scan(scan.FTP, r, mirror.FtpURL, id, nil)
			}
		}

		if err != nil {
			log.Errorf("Scanning %s failed: %s", id, err.Error())
		}

		// Finally enable the mirror if requested
		if err == nil && *enable == true {
			if err := mirrors.EnableMirror(r, id); err != nil {
				log.Fatal("Couldn't enable the mirror: ", err)
			}
			fmt.Println("Mirror enabled successfully")
		}
	}
	wg.Wait()
	return nil
}

func (c *cli) CmdRefresh(args ...string) error {
	cmd := SubCmd("refresh", "", "Scan the local repository")
	rehash := cmd.Bool("rehash", false, "Force a rehash of the files")

	if err := cmd.Parse(args); err != nil {
		return nil
	}
	if cmd.NArg() != 0 {
		cmd.Usage()
		return nil
	}

	err := scan.ScanSource(database.NewRedis(), *rehash, nil)
	return err
}

func (c *cli) matchMirror(text string) (list []string, err error) {
	if len(text) == 0 {
		return nil, errors.New("Nothing to match")
	}

	r := database.NewRedis()
	conn, err := r.Connect()
	if err != nil {
		log.Fatal("Redis: ", err)
	}
	defer conn.Close()

	list = make([]string, 0, 0)

	mirrorsIDs, err := redis.Strings(conn.Do("LRANGE", "MIRRORS", "0", "-1"))
	if err != nil {
		return nil, errors.New("Cannot fetch the list of mirrors")
	}

	for _, e := range mirrorsIDs {
		if text == e {
			return []string{e}, nil
		}
		if strings.Contains(e, text) {
			list = append(list, e)
		}
	}
	return
}

func (c *cli) CmdEdit(args ...string) error {
	cmd := SubCmd("edit", "[IDENTIFIER]", "Edit a mirror")

	if err := cmd.Parse(args); err != nil {
		return nil
	}
	if cmd.NArg() != 1 {
		cmd.Usage()
		return nil
	}

	// Find the editor to use
	editor := os.Getenv("EDITOR")

	if editor == "" {
		log.Fatal("Environment variable $EDITOR not set")
	}

	// Guess which mirror to use
	list, err := c.matchMirror(cmd.Arg(0))
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Fprintf(os.Stderr, "No match for %s\n", cmd.Arg(0))
		return nil
	} else if len(list) > 1 {
		for _, e := range list {
			fmt.Fprintf(os.Stderr, "%s\n", e)
		}
		return nil
	}

	id := list[0]

	// Connect to the database
	r := database.NewRedis()
	conn, err := r.Connect()
	if err != nil {
		log.Fatal("Redis: ", err)
	}
	defer conn.Close()

	// Get the mirror information
	key := fmt.Sprintf("MIRROR_%s", id)
	m, err := redis.Values(conn.Do("HGETALL", key))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot fetch mirror details: %s\n", err)
		return err
	}

	var mirror mirrors.Mirror
	err = redis.ScanStruct(m, &mirror)
	if err != nil {
		return err
	}

	// Generate a yaml configuration string from the struct
	out, err := yaml.Marshal(mirror)

	// Open a temporary file
	f, err := ioutil.TempFile(os.TempDir(), "edit")
	if err != nil {
		log.Fatal("Cannot create temporary file:", err)
	}
	defer os.Remove(f.Name())
	f.WriteString("# You can now edit this mirror configuration.\n" +
		"# Just save and quit when you're done.\n\n")
	f.WriteString(string(out))
	f.WriteString(fmt.Sprintf("\n%s\n\n%s\n", commentSeparator, mirror.Comment))
	f.Close()

	// Checksum the original file
	chk, _ := filesystem.Sha256sum(f.Name())

reopen:
	// Launch the editor with the filename as first parameter
	exe := exec.Command(editor, f.Name())
	exe.Stdin = os.Stdin
	exe.Stdout = os.Stdout
	exe.Stderr = os.Stderr

	err = exe.Run()
	if err != nil {
		log.Fatal(err)
	}

	// Read the file back
	out, err = ioutil.ReadFile(f.Name())
	if err != nil {
		log.Fatal("Cannot read file", f.Name())
	}

	// Checksum the file back and compare
	chk2, _ := filesystem.Sha256sum(f.Name())
	if bytes.Compare(chk, chk2) == 0 {
		fmt.Println("Aborted - settings are unmodified, so there is nothing to change.")
		return nil
	}

	var (
		yamlstr = string(out)
		comment string
	)

	commentIndex := strings.Index(yamlstr, commentSeparator)
	if commentIndex > 0 {
		comment = strings.TrimSpace(yamlstr[commentIndex+len(commentSeparator):])
		yamlstr = yamlstr[:commentIndex]
	}

	// Fill the struct from the yaml
	err = yaml.Unmarshal([]byte(yamlstr), &mirror)
	if err != nil {
	eagain:
		fmt.Printf("%s\nRetry? [Y/n]", err.Error())
		reader := bufio.NewReader(os.Stdin)
		s, _ := reader.ReadString('\n')
		switch s[0] {
		case 'y', 'Y', 10:
			goto reopen
		case 'n', 'N':
			fmt.Println("Aborted")
			return nil
		default:
			goto eagain
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

	mirror.Comment = comment

	// Save the values back into redis
	_, err = conn.Do("HMSET", key,
		"ID", id,
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

	if err != nil {
		log.Fatal("Couldn't save the configuration into redis:", err)
	}

	// Publish update
	database.Publish(conn, database.MIRROR_UPDATE, id)

	fmt.Println("Mirror edited successfully")

	return nil
}

func (c *cli) CmdShow(args ...string) error {
	cmd := SubCmd("show", "[IDENTIFIER]", "Print a mirror configuration")

	if err := cmd.Parse(args); err != nil {
		return nil
	}
	if cmd.NArg() != 1 {
		cmd.Usage()
		return nil
	}

	// Guess which mirror to use
	list, err := c.matchMirror(cmd.Arg(0))
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Fprintf(os.Stderr, "No match for %s\n", cmd.Arg(0))
		return nil
	} else if len(list) > 1 {
		for _, e := range list {
			fmt.Fprintf(os.Stderr, "%s\n", e)
		}
		return nil
	}

	id := list[0]

	// Connect to the database
	r := database.NewRedis()
	conn, err := r.Connect()
	if err != nil {
		log.Fatal("Redis: ", err)
	}
	defer conn.Close()

	// Get the mirror information
	key := fmt.Sprintf("MIRROR_%s", id)
	m, err := redis.Values(conn.Do("HGETALL", key))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot fetch mirror details: %s\n", err)
		return err
	}

	var mirror mirrors.Mirror
	err = redis.ScanStruct(m, &mirror)
	if err != nil {
		return err
	}

	// Generate a yaml configuration string from the struct
	out, err := yaml.Marshal(mirror)

	fmt.Printf("Mirror: %s\n%s\nComment:\n%s\n", id, out, mirror.Comment)
	return err
}

func (c *cli) CmdExport(args ...string) error {
	cmd := SubCmd("export", "[format]", "Export the mirror database.\n\nAvailable formats: mirmon")
	rsync := cmd.Bool("rsync", true, "Export rsync URLs")
	http := cmd.Bool("http", true, "Export http URLs")
	ftp := cmd.Bool("ftp", true, "Export ftp URLs")
	disabled := cmd.Bool("disabled", true, "Export disabled mirrors")

	if err := cmd.Parse(args); err != nil {
		return nil
	}
	if cmd.NArg() != 1 {
		cmd.Usage()
		return nil
	}

	if cmd.Arg(0) != "mirmon" {
		fmt.Fprintf(os.Stderr, "Unsupported format\n")
		cmd.Usage()
		return nil
	}

	r := database.NewRedis()
	conn, err := r.Connect()
	if err != nil {
		log.Fatal("Redis: ", err)
	}
	defer conn.Close()

	mirrorsIDs, err := redis.Strings(conn.Do("LRANGE", "MIRRORS", "0", "-1"))
	if err != nil {
		log.Fatal("Cannot fetch the list of mirrors: ", err)
	}

	conn.Send("MULTI")

	for _, e := range mirrorsIDs {
		conn.Send("HGETALL", fmt.Sprintf("MIRROR_%s", e))
	}

	res, err := redis.Values(conn.Do("EXEC"))
	if err != nil {
		log.Fatal("Redis: ", err)
	}

	w := new(tabwriter.Writer)
	w.Init(os.Stdout, 0, 8, 0, '\t', 0)

	for _, e := range res {
		var mirror mirrors.Mirror
		res, ok := e.([]interface{})
		if !ok {
			log.Fatal("Typecast failed")
		} else {
			err := redis.ScanStruct([]interface{}(res), &mirror)
			if err != nil {
				log.Fatal("ScanStruct:", err)
			}
			if *disabled == false {
				if mirror.Enabled == false {
					continue
				}
			}
			ccodes := strings.Fields(mirror.CountryCodes)

			urls := make([]string, 0, 3)
			if *rsync == true && mirror.RsyncURL != "" {
				urls = append(urls, mirror.RsyncURL)
			}
			if *http == true && mirror.HttpURL != "" {
				urls = append(urls, mirror.HttpURL)
			}
			if *ftp == true && mirror.FtpURL != "" {
				urls = append(urls, mirror.FtpURL)
			}

			for _, u := range urls {
				fmt.Fprintf(w, "%s\t%s\t%s\n", ccodes[0], u, mirror.AdminEmail)
			}
		}
	}

	w.Flush()

	return nil
}

func (c *cli) CmdEnable(args ...string) error {
	cmd := SubCmd("enable", "[IDENTIFIER]", "Enable a mirror")

	if err := cmd.Parse(args); err != nil {
		return nil
	}
	if cmd.NArg() != 1 {
		cmd.Usage()
		return nil
	}

	// Guess which mirror to use

	list, err := c.matchMirror(cmd.Arg(0))
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Fprintf(os.Stderr, "No match for %s\n", cmd.Arg(0))
		return nil
	} else if len(list) > 1 {
		for _, e := range list {
			fmt.Fprintf(os.Stderr, "%s\n", e)
		}
		return nil
	}

	err = mirrors.EnableMirror(database.NewRedis(), list[0])
	if err != nil {
		log.Fatal("Couldn't enable the mirror:", err)
	}

	fmt.Println("Mirror enabled successfully")

	return nil
}

func (c *cli) CmdDisable(args ...string) error {
	cmd := SubCmd("disable", "[IDENTIFIER]", "Disable a mirror")

	if err := cmd.Parse(args); err != nil {
		return nil
	}
	if cmd.NArg() != 1 {
		cmd.Usage()
		return nil
	}

	// Guess which mirror to use

	list, err := c.matchMirror(cmd.Arg(0))
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Fprintf(os.Stderr, "No match for %s\n", cmd.Arg(0))
		return nil
	} else if len(list) > 1 {
		for _, e := range list {
			fmt.Fprintf(os.Stderr, "%s\n", e)
		}
		return nil
	}

	err = mirrors.DisableMirror(database.NewRedis(), list[0])
	if err != nil {
		log.Fatal("Couldn't disable the mirror:", err)
	}

	fmt.Println("Mirror disabled successfully")

	return nil
}

func (c *cli) CmdStats(args ...string) error {
	cmd := SubCmd("stats", "[OPTIONS] [mirror|file] [IDENTIFIER|PATTERN]", "Show download stats for a particular mirror or a file pattern")
	dateStart := cmd.String("start-date", "", "Starting date (format YYYY-MM-DD)")
	dateEnd := cmd.String("end-date", "", "Ending date (format YYYY-MM-DD)")
	human := cmd.Bool("h", true, "Human readable version")

	if err := cmd.Parse(args); err != nil {
		return nil
	}
	if cmd.NArg() != 2 || (cmd.Arg(0) != "mirror" && cmd.Arg(0) != "file") {
		cmd.Usage()
		return nil
	}

	r := database.NewRedis()
	conn, err := r.Connect()
	if err != nil {
		log.Fatal("Redis: ", err)
	}
	defer conn.Close()

	start, err := time.Parse("2006-1-2", *dateStart)
	if err != nil {
		start = time.Now()
	}

	end, err := time.Parse("2006-1-2", *dateEnd)
	if err != nil {
		end = time.Now()
	}

	tkcoverage := utils.TimeKeyCoverage(start, end)
	for _, b := range tkcoverage {
		log.Debugf("Requesting %s", b)
	}

	if cmd.Arg(0) == "file" {
		// File stats

		re, err := regexp.Compile(cmd.Arg(1))
		if err != nil {
			return err
		}

		conn.Send("MULTI")

		for _, k := range tkcoverage {
			conn.Send("HGETALL", "STATS_FILE_"+k)
		}

		stats, err := redis.Values(conn.Do("EXEC"))
		if err != nil {
			log.Critical("Cannot fetch stats: %s", err)
			return err
		}

		var requests int64
		m := make(map[string]int64)

		for _, res := range stats {
			line, ok := res.([]interface{})
			if !ok {
				log.Fatal("Typecast failed")
			} else {
				stats := []interface{}(line)
				for i := 0; i < len(stats); i += 2 {
					path, _ := redis.String(stats[i], nil)
					matched := re.MatchString(path)
					if err != nil {
						log.Error(err.Error())
					} else if matched {
						reqs, _ := redis.Int64(stats[i+1], nil)
						m[path] += reqs
						requests += reqs
					}
				}
			}
		}

		// Format the results
		w := new(tabwriter.Writer)
		w.Init(os.Stdout, 0, 8, 0, '\t', 0)

		// Sort keys
		var keys []string
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			fmt.Fprintf(w, "%s:\t%d\n", k, m[k])
		}

		if len(keys) > 0 {
			// Add a line separator
			fmt.Fprintf(w, "\t\n")
		}

		fmt.Fprintf(w, "Total download requests:\t%d\n", requests)
		w.Flush()
	} else if cmd.Arg(0) == "mirror" {
		// Mirror stats

		list, err := c.matchMirror(cmd.Arg(1))
		if err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Fprintf(os.Stderr, "No match for mirror %s\n", cmd.Arg(1))
			return nil
		} else if len(list) > 1 {
			for _, e := range list {
				fmt.Fprintf(os.Stderr, "%s\n", e)
			}
			return nil
		}

		conn.Send("MULTI")

		// Fetch the stats
		for _, k := range tkcoverage {
			conn.Send("HGET", "STATS_MIRROR_"+k, list[0])
			conn.Send("HGET", "STATS_MIRROR_BYTES_"+k, list[0])
		}

		stats, err := redis.Strings(conn.Do("EXEC"))
		if err != nil {
			log.Critical("Cannot fetch stats: %s", err)
			return err
		}

		// Fetch the mirror struct
		m, err := redis.Values(conn.Do("HGETALL", fmt.Sprintf("MIRROR_%s", list[0])))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Cannot fetch mirror details: %s\n", err)
			return err
		}

		var mirror mirrors.Mirror
		err = redis.ScanStruct(m, &mirror)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Cannot fetch mirror details: %s\n", err)
			return err
		}

		var (
			requests int64
			bytes    int64
		)

		for i := 0; i < len(stats); i += 2 {
			v1, _ := strconv.ParseInt(stats[i], 10, 64)
			v2, _ := strconv.ParseInt(stats[i+1], 10, 64)
			requests += v1
			bytes += v2
		}

		// Format the results
		w := new(tabwriter.Writer)
		w.Init(os.Stdout, 0, 8, 0, '\t', 0)

		fmt.Fprintf(w, "Identifier:\t%s\n", list[0])
		if !mirror.Enabled {
			fmt.Fprintf(w, "Status:\tdisabled\n")
		} else if mirror.Up {
			fmt.Fprintf(w, "Status:\tup\n")
		} else {
			fmt.Fprintf(w, "Status:\tdown\n")
		}
		fmt.Fprintf(w, "Download requests:\t%d\n", requests)
		fmt.Fprint(w, "Bytes transferred:\t")
		if *human {
			fmt.Fprintln(w, utils.ReadableSize(bytes))
		} else {
			fmt.Fprintln(w, bytes)
		}
		w.Flush()
	}

	return nil
}

func (c *cli) CmdReload(args ...string) error {
	pid := process.GetRemoteProcPid()
	if pid > 0 {
		err := syscall.Kill(pid, syscall.SIGHUP)
		if err != nil {
			log.Errorf("Unable to reload configuration: %v", err)
		}
	} else {
		log.Error("No pid found. Ensures the server is running.")
	}
	return nil
}

func (c *cli) CmdUpgrade(args ...string) error {
	pid := process.GetRemoteProcPid()
	if pid > 0 {
		err := syscall.Kill(pid, syscall.SIGUSR2)
		if err != nil {
			log.Errorf("Unable to upgrade binary: %v", err)
		}
	} else {
		log.Error("No pid found. Ensures the server is running.")
	}
	return nil
}

func (c *cli) CmdVersion(args ...string) error {
	core.PrintVersion()
	return nil
}
