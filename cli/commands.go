// Copyright (c) 2014-2019 Ludovic Fauvet
// Licensed under the MIT license

package cli

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/etix/mirrorbits/core"
	"github.com/etix/mirrorbits/filesystem"
	"github.com/etix/mirrorbits/mirrors"
	"github.com/etix/mirrorbits/rpc"
	"github.com/etix/mirrorbits/utils"
	"github.com/golang/protobuf/ptypes"
	"github.com/golang/protobuf/ptypes/empty"
	"github.com/howeyc/gopass"
	"github.com/op/go-logging"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gopkg.in/yaml.v3"
)

const (
	commentSeparator  = "##### Comments go below this line #####"
	defaultRPCTimeout = time.Second * 10
)

var (
	log = logging.MustGetLogger("main")
)

type cli struct {
	sync.Mutex
	rpcconn *grpc.ClientConn
	creds   *loginCreds
}

// ParseCommands parses the command line and call the appropriate functions
func ParseCommands(args ...string) error {
	c := &cli{
		creds: &loginCreds{
			Password: core.RPCPassword,
		},
	}

	if len(args) > 0 && args[0] != "help" {
		method, exists := c.getMethod(args[0])
		if !exists {
			fmt.Println("Error: Command not found:", args[0])
			return c.CmdHelp()
		}
		if len(c.creds.Password) == 0 && core.RPCAskPass {
			fmt.Print("Password: ")
			passwd, err := gopass.GetPasswdMasked()
			if err != nil {
				return err
			}
			c.creds.Password = string(passwd)
		}
		ret := method.Func.CallSlice([]reflect.Value{
			reflect.ValueOf(c),
			reflect.ValueOf(args[1:]),
		})[0].Interface()
		if c.rpcconn != nil {
			c.rpcconn.Close()
		}
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
	help := fmt.Sprintf("Usage: mirrorbits [OPTIONS] COMMAND [arg...]\n\nA smart download redirector.\n\n")
	help += fmt.Sprintf("Server commands:\n    %-10.10s%s\n\n", "daemon", "Start the server")
	help += fmt.Sprintf("CLI commands:\n")
	for _, command := range [][]string{
		{"add", "Add a new mirror"},
		{"disable", "Disable a mirror"},
		{"edit", "Edit a mirror"},
		{"enable", "Enable a mirror"},
		{"export", "Export the mirror database"},
		{"geoupdate", "Update geolocation of a mirror"},
		{"list", "List all mirrors"},
		{"logs", "Print logs of a mirror"},
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

type ByDate []*rpc.Mirror

func (d ByDate) Len() int           { return len(d) }
func (d ByDate) Swap(i, j int)      { d[i], d[j] = d[j], d[i] }
func (d ByDate) Less(i, j int) bool { return d[i].StateSince.Seconds > d[j].StateSince.Seconds }

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

	client := c.GetRPC()
	ctx, cancel := context.WithTimeout(context.Background(), defaultRPCTimeout)
	defer cancel()
	list, err := client.List(ctx, &empty.Empty{})
	if err != nil {
		log.Fatal("list error:", err)
	}

	sort.Sort(ByDate(list.Mirrors))

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

	for _, mirror := range list.Mirrors {
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
			if mirror.Up == true || mirror.Enabled == false {
				continue
			}
		}
		stateSince, err := ptypes.Timestamp(mirror.StateSince)
		if err != nil {
			log.Fatal("list error:", err)
		}
		fmt.Fprintf(w, "%s ", mirror.Name)
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
			fmt.Fprintf(w, " \t(%s)", stateSince.Format(time.RFC1123))
		}
		fmt.Fprint(w, "\n")
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

	if !utils.HasAnyPrefix(*http, "http://", "https://") {
		*http = "http://" + *http
	}

	_, err := url.Parse(*http)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Can't parse url\n")
		os.Exit(-1)
	}

	mirror := &mirrors.Mirror{
		Name:           cmd.Arg(0),
		HttpURL:        *http,
		RsyncURL:       *rsync,
		FtpURL:         *ftp,
		SponsorName:    *sponsorName,
		SponsorURL:     *sponsorURL,
		SponsorLogoURL: *sponsorLogo,
		AdminName:      *adminName,
		AdminEmail:     *adminEmail,
		CustomData:     *customData,
		ContinentOnly:  *continentOnly,
		CountryOnly:    *countryOnly,
		ASOnly:         *asOnly,
		Score:          *score,
		Comment:        *comment,
	}

	client := c.GetRPC()
	ctx, cancel := context.WithTimeout(context.Background(), defaultRPCTimeout)
	defer cancel()
	m, err := rpc.MirrorToRPC(mirror)
	if err != nil {
		log.Fatal("edit error:", err)
	}
	reply, err := client.AddMirror(ctx, m)
	if err != nil {
		if err.Error() == rpc.ErrNameAlreadyTaken.Error() {
			log.Fatalf("Mirror %s already exists!\n", mirror.Name)
		}
		log.Fatal("edit error:", err)
	}

	for i := 0; i < len(reply.Warnings); i++ {
		fmt.Println(reply.Warnings[i])
		if i == len(reply.Warnings)-1 {
			fmt.Println("")
		}
	}

	if reply.Country != "" {
		fmt.Println("Mirror location:")
		fmt.Printf("Latitude:  %.4f\n", reply.Latitude)
		fmt.Printf("Longitude: %.4f\n", reply.Longitude)
		fmt.Printf("Continent: %s\n", reply.Continent)
		fmt.Printf("Country:   %s\n", reply.Country)
		fmt.Printf("ASN:       %s\n", reply.ASN)
		fmt.Println("")
	}

	fmt.Printf("Mirror '%s' added successfully\n", mirror.Name)
	fmt.Printf("Enable this mirror using\n  $ mirrorbits enable %s\n", mirror.Name)

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

	id, name := c.matchMirror(cmd.Arg(0))

	if *force == false {
		fmt.Printf("Removing %s, are you sure? [y/N]", name)
		reader := bufio.NewReader(os.Stdin)
		s, _ := reader.ReadString('\n')
		switch s[0] {
		case 'y', 'Y':
			break
		default:
			return nil
		}
	}

	client := c.GetRPC()
	ctx, cancel := context.WithTimeout(context.Background(), defaultRPCTimeout)
	defer cancel()
	_, err := client.RemoveMirror(ctx, &rpc.MirrorIDRequest{
		ID: int32(id),
	})
	if err != nil {
		log.Fatal("remove error:", err)
	}

	fmt.Printf("Mirror '%s' removed successfully\n", name)
	return nil
}

func (c *cli) CmdScan(args ...string) error {
	cmd := SubCmd("scan", "[IDENTIFIER]", "(Re-)Scan a mirror")
	enable := cmd.Bool("enable", false, "Enable the mirror automatically if the scan is successful")
	all := cmd.Bool("all", false, "Scan all mirrors at once")
	ftp := cmd.Bool("ftp", false, "Force a scan using FTP")
	rsync := cmd.Bool("rsync", false, "Force a scan using rsync")
	timeout := cmd.Uint("timeout", 0, "Timeout in seconds")

	if err := cmd.Parse(args); err != nil {
		return nil
	}
	if !*all && cmd.NArg() != 1 || *all && cmd.NArg() != 0 {
		cmd.Usage()
		return nil
	}

	client := c.GetRPC()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	list := make(map[int]string)

	// Get the list of mirrors to scan
	if *all == true {
		reply, err := client.MatchMirror(ctx, &rpc.MatchRequest{
			Pattern: "", // Match all of them
		})
		if err != nil {
			return errors.New("Cannot fetch the list of mirrors")
		}

		for _, m := range reply.Mirrors {
			list[int(m.ID)] = m.Name
		}
	} else {
		// Single mirror
		id, name := c.matchMirror(cmd.Arg(0))
		list[id] = name
	}

	// Set the method of the scan (if not default)
	var method rpc.ScanMirrorRequest_Method
	if *ftp == false && *rsync == false {
		method = rpc.ScanMirrorRequest_ALL
	} else if *rsync == true {
		method = rpc.ScanMirrorRequest_RSYNC
	} else if *ftp == true {
		method = rpc.ScanMirrorRequest_FTP
	}

	for id, name := range list {

		if *timeout > 0 {
			ctx, cancel = context.WithTimeout(context.Background(), time.Duration(*timeout)*time.Second)
			defer cancel()
		}

		fmt.Printf("Scanning %s... ", name)

		reply, err := client.ScanMirror(ctx, &rpc.ScanMirrorRequest{
			ID:         int32(id),
			AutoEnable: *enable,
			Protocol:   method,
		})
		if err != nil {
			s := status.Convert(err)
			if s.Code() == codes.FailedPrecondition || len(list) == 1 {
				return errors.New("\nscan error: " + grpc.ErrorDesc(err))
			}
			fmt.Println("scan error:", grpc.ErrorDesc(err))
			continue
		} else {
			fmt.Printf("%d files indexed, %d known and %d removed\n", reply.FilesIndexed, reply.KnownIndexed, reply.Removed)
			if reply.GetTZOffsetMs() != 0 {
				fmt.Printf("  ∟ Timezone offset detected and corrected: %d milliseconds\n", reply.TZOffsetMs)
			}
			if reply.Enabled {
				fmt.Println("  ∟ Enabled")
			}
		}
	}

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

	fmt.Print("Refreshing the local repository... ")

	client := c.GetRPC()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := client.RefreshRepository(ctx, &rpc.RefreshRepositoryRequest{
		Rehash: *rehash,
	})
	if err != nil {
		fmt.Println("")
		log.Fatal(err)
	}

	fmt.Println("done")

	return nil
}

func (c *cli) matchMirror(pattern string) (id int, name string) {
	if len(pattern) == 0 {
		return -1, ""
	}

	client := c.GetRPC()
	ctx, cancel := context.WithTimeout(context.Background(), defaultRPCTimeout)
	defer cancel()
	reply, err := client.MatchMirror(ctx, &rpc.MatchRequest{
		Pattern: pattern,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "mirror matching: %s\n", err)
		os.Exit(1)
	}

	switch len(reply.Mirrors) {
	case 0:
		fmt.Fprintf(os.Stderr, "No match for '%s'\n", pattern)
		os.Exit(1)
	case 1:
		id, name, err := GetSingle(reply.Mirrors)
		if err != nil {
			log.Fatal("unexpected error:", err)
		}
		return id, name
	default:
		fmt.Fprintln(os.Stderr, "Multiple match:")
		for _, mirror := range reply.Mirrors {
			fmt.Fprintf(os.Stderr, "  %s\n", mirror.Name)
		}
		os.Exit(1)
	}
	return
}

func GetSingle(list []*rpc.MirrorID) (int, string, error) {
	if len(list) == 0 {
		return -1, "", errors.New("list is empty")
	} else if len(list) > 1 {
		return -1, "", errors.New("too many results")
	}
	return int(list[0].ID), list[0].Name, nil
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
		for _, p := range []string{"editor", "vi", "emacs", "nano"} {
			_, err := exec.LookPath(p)
			if err == nil {
				editor = p
				break
			}
		}
		if editor == "" {
			log.Fatal("No text editor found, please set the EDITOR environment variable")
		}
	}

	id, _ := c.matchMirror(cmd.Arg(0))

	client := c.GetRPC()
	ctx, cancel := context.WithTimeout(context.Background(), defaultRPCTimeout)
	defer cancel()
	rpcm, err := client.MirrorInfo(ctx, &rpc.MirrorIDRequest{
		ID: int32(id),
	})
	if err != nil {
		log.Fatal("edit error:", err)
	}
	mirror, err := rpc.MirrorFromRPC(rpcm)
	if err != nil {
		log.Fatal("edit error:", err)
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

	var comment string
	yamlstr := string(out)

	commentIndex := strings.Index(yamlstr, commentSeparator)
	if commentIndex > 0 {
		comment = strings.TrimSpace(yamlstr[commentIndex+len(commentSeparator):])
		yamlstr = yamlstr[:commentIndex]
	}

	reopen := func(err error) bool {
	eagain:
		fmt.Printf("%s\nRetry? [Y/n]", err.Error())
		reader := bufio.NewReader(os.Stdin)
		s, _ := reader.ReadString('\n')
		switch s[0] {
		case 'y', 'Y', 10:
			return true
		case 'n', 'N':
			fmt.Println("Aborted")
			return false
		default:
			goto eagain
		}
	}

	// Fill the struct from the yaml
	err = yaml.Unmarshal([]byte(yamlstr), &mirror)
	if err != nil {
		switch reopen(err) {
		case true:
			goto reopen
		case false:
			return nil
		}
	}

	mirror.Comment = comment

	ctx, cancel = context.WithTimeout(context.Background(), defaultRPCTimeout)
	defer cancel()
	m, err := rpc.MirrorToRPC(mirror)
	if err != nil {
		log.Fatal("edit error:", err)
	}
	reply, err := client.UpdateMirror(ctx, m)
	if err != nil {
		if err.Error() == rpc.ErrNameAlreadyTaken.Error() {
			switch reopen(errors.New("Name already taken")) {
			case true:
				goto reopen
			case false:
				return nil
			}
		}
		log.Fatal("edit error:", err)
	}

	if len(reply.Diff) > 0 {
		fmt.Println(reply.Diff)
	}

	fmt.Printf("Mirror '%s' edited successfully\n", mirror.Name)

	return nil
}

func (c *cli) CmdGeoupdate(args ...string) error {
	cmd := SubCmd("geoupdate", "[IDENTIFIER]", "Update geolocation of a mirror")
	force := cmd.Bool("f", false, "Never prompt for confirmation")

	if err := cmd.Parse(args); err != nil {
		return nil
	}
	if cmd.NArg() != 1 {
		cmd.Usage()
		return nil
	}

	id, name := c.matchMirror(cmd.Arg(0))

	// Get mirror with geolocation updated
	client := c.GetRPC()
	ctx, cancel := context.WithTimeout(context.Background(), defaultRPCTimeout)
	defer cancel()
	reply, err := client.GeoUpdateMirror(ctx, &rpc.MirrorIDRequest{
		ID: int32(id),
	})
	if err != nil {
		log.Fatal("edit error:", err)
	}

	// Print warnings if any
	for i := 0; i < len(reply.Warnings); i++ {
		fmt.Println(reply.Warnings[i])
		if i == len(reply.Warnings)-1 {
			fmt.Println("")
		}
	}

	// Print diff if any
	if len(reply.Diff) > 0 {
		fmt.Println(reply.Diff)
	} else {
		fmt.Println("Geolocation is up to date, there is nothing to change.")
		return nil
	}

	// Ask for confirmation
	if *force == false {
		fmt.Printf("Update mirror %s? [y/N]", name)
		reader := bufio.NewReader(os.Stdin)
		s, _ := reader.ReadString('\n')
		switch s[0] {
		case 'y', 'Y':
			break
		default:
			return nil
		}
	}

	// Update the mirror
	ctx, cancel = context.WithTimeout(context.Background(), defaultRPCTimeout)
	defer cancel()
	reply2, err := client.UpdateMirror(ctx, reply.Mirror)
	if err != nil {
		log.Fatal("edit error:", err)
	}

	// The diff shouldn't have changed, but let's check
	if reply2.Diff != reply.Diff {
		fmt.Println("Unexpected diff, see below:")
		fmt.Println(reply2.Diff)
	}

	fmt.Printf("Mirror '%s' updated successfully\n", name)

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

	id, _ := c.matchMirror(cmd.Arg(0))

	client := c.GetRPC()
	ctx, cancel := context.WithTimeout(context.Background(), defaultRPCTimeout)
	defer cancel()
	rpcm, err := client.MirrorInfo(ctx, &rpc.MirrorIDRequest{
		ID: int32(id),
	})
	if err != nil {
		log.Fatal("edit error:", err)
	}
	mirror, err := rpc.MirrorFromRPC(rpcm)
	if err != nil {
		log.Fatal("edit error:", err)
	}

	// Generate a yaml configuration string from the struct
	out, err := yaml.Marshal(mirror)
	if err != nil {
		log.Fatal("show error:", err)
	}

	fmt.Printf("%s\nComment:\n%s\n", out, mirror.Comment)
	return nil
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

	client := c.GetRPC()
	ctx, cancel := context.WithTimeout(context.Background(), defaultRPCTimeout)
	defer cancel()
	list, err := client.List(ctx, &empty.Empty{})
	if err != nil {
		log.Fatal("export error:", err)
	}

	w := new(tabwriter.Writer)
	w.Init(os.Stdout, 0, 8, 0, '\t', 0)

	for _, m := range list.Mirrors {
		if *disabled == false {
			if m.Enabled == false {
				continue
			}
		}
		ccodes := strings.Fields(m.CountryCodes)

		urls := make([]string, 0, 3)
		if *rsync == true && m.RsyncURL != "" {
			urls = append(urls, m.RsyncURL)
		}
		if *http == true && m.HttpURL != "" {
			urls = append(urls, m.HttpURL)
		}
		if *ftp == true && m.FtpURL != "" {
			urls = append(urls, m.FtpURL)
		}

		for _, u := range urls {
			fmt.Fprintf(w, "%s\t%s\t%s\n", ccodes[0], u, m.AdminEmail)
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

	c.changeStatus(cmd.Arg(0), true)
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

	c.changeStatus(cmd.Arg(0), false)
	return nil
}

func (c *cli) changeStatus(pattern string, enabled bool) {
	id, name := c.matchMirror(pattern)

	client := c.GetRPC()
	ctx, cancel := context.WithTimeout(context.Background(), defaultRPCTimeout)
	defer cancel()
	_, err := client.ChangeStatus(ctx, &rpc.ChangeStatusRequest{
		ID:      int32(id),
		Enabled: enabled,
	})
	if err != nil {
		if enabled {
			log.Fatalf("Couldn't enable mirror '%s': %s\n", name, err)
		} else {
			log.Fatalf("Couldn't disable mirror '%s': %s\n", name, err)
		}
	}

	if enabled {
		fmt.Printf("Mirror '%s' enabled successfully\n", name)
	} else {
		fmt.Printf("Mirror '%s' disabled successfully\n", name)
	}
	return
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

	start, err := time.Parse("2006-1-2", *dateStart)
	if err != nil {
		start = time.Now()
	}
	startproto, _ := ptypes.TimestampProto(start)

	end, err := time.Parse("2006-1-2", *dateEnd)
	if err != nil {
		end = time.Now()
	}
	endproto, _ := ptypes.TimestampProto(end)

	client := c.GetRPC()
	ctx, cancel := context.WithTimeout(context.Background(), defaultRPCTimeout)
	defer cancel()

	if cmd.Arg(0) == "file" {
		// File stats

		reply, err := client.StatsFile(ctx, &rpc.StatsFileRequest{
			Pattern:   cmd.Arg(1),
			DateStart: startproto,
			DateEnd:   endproto,
		})
		if err != nil {
			log.Fatal("file stats error:", err)
		}

		// Format the results
		w := new(tabwriter.Writer)
		w.Init(os.Stdout, 0, 8, 0, '\t', 0)

		// Sort keys and count requests
		var keys []string
		var requests int64
		for k, req := range reply.Files {
			requests += req
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			fmt.Fprintf(w, "%s:\t%d\n", k, reply.Files[k])
		}

		if len(keys) > 0 {
			// Add a line separator
			fmt.Fprintf(w, "\t\n")
		}

		fmt.Fprintf(w, "Total download requests: \t%d\n", requests)
		w.Flush()
	} else if cmd.Arg(0) == "mirror" {
		// Mirror stats

		id, name := c.matchMirror(cmd.Arg(1))

		reply, err := client.StatsMirror(ctx, &rpc.StatsMirrorRequest{
			ID:        int32(id),
			DateStart: startproto,
			DateEnd:   endproto,
		})
		if err != nil {
			log.Fatal("mirror stats error:", err)
		}

		// Format the results
		w := new(tabwriter.Writer)
		w.Init(os.Stdout, 0, 8, 0, '\t', 0)

		fmt.Fprintf(w, "Identifier:\t%s\n", name)
		if !reply.Mirror.Enabled {
			fmt.Fprintf(w, "Status:\tdisabled\n")
		} else if reply.Mirror.Up {
			fmt.Fprintf(w, "Status:\tup\n")
		} else {
			fmt.Fprintf(w, "Status:\tdown\n")
		}
		fmt.Fprintf(w, "Download requests:\t%d\n", reply.Requests)
		fmt.Fprint(w, "Bytes transferred:\t")
		if *human {
			fmt.Fprintln(w, utils.ReadableSize(reply.Bytes))
		} else {
			fmt.Fprintln(w, reply.Bytes)
		}
		w.Flush()
	}

	return nil
}

func (c *cli) CmdLogs(args ...string) error {
	cmd := SubCmd("logs", "[IDENTIFIER]", "Print logs of a mirror")
	maxResults := cmd.Uint("l", 500, "Maximum number of logs to return")

	if err := cmd.Parse(args); err != nil {
		return nil
	}
	if cmd.NArg() != 1 {
		cmd.Usage()
		return nil
	}

	id, name := c.matchMirror(cmd.Arg(0))

	client := c.GetRPC()
	ctx, cancel := context.WithTimeout(context.Background(), defaultRPCTimeout)
	defer cancel()
	resp, err := client.GetMirrorLogs(ctx, &rpc.GetMirrorLogsRequest{
		ID:         int32(id),
		MaxResults: int32(*maxResults),
	})
	if err != nil {
		log.Fatal("logs error:", err)
	}

	if len(resp.Line) == 0 {
		fmt.Printf("No logs for %s\n", name)
		return nil
	}

	fmt.Printf("Printing logs for %s:\n", name)

	for _, l := range resp.Line {
		fmt.Println(l)
	}

	return nil
}

func (c *cli) CmdReload(args ...string) error {
	cmd := SubCmd("reload", "", "Reload configuration")

	if err := cmd.Parse(args); err != nil {
		return nil
	}
	if cmd.NArg() != 0 {
		cmd.Usage()
		return nil
	}

	client := c.GetRPC()
	ctx, cancel := context.WithTimeout(context.Background(), defaultRPCTimeout)
	defer cancel()
	_, err := client.Reload(ctx, &empty.Empty{})
	if err != nil {
		log.Fatal("reload error:", err)
	}

	return nil
}

func (c *cli) CmdUpgrade(args ...string) error {
	cmd := SubCmd("upgrade", "", "Seamless binary upgrade")

	if err := cmd.Parse(args); err != nil {
		return nil
	}
	if cmd.NArg() != 0 {
		cmd.Usage()
		return nil
	}

	client := c.GetRPC()
	ctx, cancel := context.WithTimeout(context.Background(), defaultRPCTimeout)
	defer cancel()
	_, err := client.Upgrade(ctx, &empty.Empty{})
	if err != nil {
		log.Fatal("upgrade error:", err)
	}

	return nil
}

func (c *cli) CmdVersion(args ...string) error {
	cmd := SubCmd("version", "", "Print version information")

	if err := cmd.Parse(args); err != nil {
		return nil
	}
	if cmd.NArg() != 0 {
		cmd.Usage()
		return nil
	}

	fmt.Printf("Client:\n")
	core.PrintVersion(core.GetVersionInfo())
	fmt.Println()

	client := c.GetRPC()
	ctx, cancel := context.WithTimeout(context.Background(), defaultRPCTimeout)
	defer cancel()
	reply, err := client.GetVersion(ctx, &empty.Empty{})
	if err != nil {
		s := status.Convert(err)
		return errors.Wrap(s.Err(), "version error")
	}

	if reply.Version != "" {
		fmt.Printf("Server:\n")
		core.PrintVersion(core.VersionInfo{
			Version:    reply.Version,
			Build:      reply.Build,
			GoVersion:  reply.GoVersion,
			OS:         reply.OS,
			Arch:       reply.Arch,
			GoMaxProcs: int(reply.GoMaxProcs),
		})
	}
	return nil
}
