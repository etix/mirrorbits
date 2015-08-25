// Copyright (c) 2014-2015 Ludovic Fauvet
// Licensed under the MIT license

package main

import (
	"fmt"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"os"
	"strings"
	"sync"
)

var (
	defaultConfig = configuration{
		Repository:             "",
		Templates:              "",
		OutputMode:             "auto",
		ListenAddress:          ":8080",
		Gzip:                   false,
		RedisAddress:           "127.0.0.1:6379",
		RedisPassword:          "",
		LogDir:                 "",
		GeoipDatabasePath:      "/usr/share/GeoIP/",
		DownloadStatsPath:      "",
		ConcurrentSync:         2,
		ScanInterval:           30,
		CheckInterval:          1,
		RepositoryScanInterval: 5,
		Hashes: hashing{
			SHA1:   true,
			SHA256: false,
			MD5:    false,
		},
		DisallowRedirects:       false,
		WeightDistributionRange: 1.5,
		DisableOnMissingFile:    false,
	}
	config      *configuration
	configMutex sync.RWMutex
)

type configuration struct {
	Repository              string     `yaml:"Repository"`
	Templates               string     `yaml:"Templates"`
	OutputMode              string     `yaml:"OutputMode"`
	ListenAddress           string     `yaml:"ListenAddress"`
	Gzip                    bool       `yaml:"Gzip"`
	RedisAddress            string     `yaml:"RedisAddress"`
	RedisPassword           string     `yaml:"RedisPassword"`
	LogDir                  string     `yaml:"LogDir"`
	GeoipDatabasePath       string     `yaml:"GeoipDatabasePath"`
	ConcurrentSync          int        `yaml:"ConcurrentSync"`
	ScanInterval            int        `yaml:"ScanInterval"`
	CheckInterval           int        `yaml:"CheckInterval"`
	RepositoryScanInterval  int        `yaml:"RepositoryScanInterval"`
	Hashes                  hashing    `yaml:"Hashes"`
	DisallowRedirects       bool       `yaml:"DisallowRedirects"`
	WeightDistributionRange float32    `yaml:"WeightDistributionRange"`
	DisableOnMissingFile    bool       `yaml:"DisableOnMissingFile"`
	Fallbacks               []fallback `yaml:"Fallbacks"`
	DownloadStatsPath       string     `yaml:"DownloadStatsPath"`

	RedisSentinelMasterName string      `yaml:"RedisSentinelMasterName"`
	RedisSentinels          []sentinels `yaml:"RedisSentinels"`
}

type fallback struct {
	Url           string `yaml:"URL"`
	CountryCode   string `yaml:"CountryCode"`
	ContinentCode string `yaml:"ContinentCode"`
}

type sentinels struct {
	Host string `yaml:"Host"`
}

type hashing struct {
	SHA1   bool `yaml:"SHA1"`
	SHA256 bool `yaml:"SHA256"`
	MD5    bool `yaml:"MD5"`
}

// LoadConfig loads the configuration file if it has not yet been loaded
func LoadConfig() {
	if config != nil {
		return
	}
	err := ReloadConfig()
	if err != nil {
		log.Fatal(err)
	}
}

// ReloadConfig reloads the configuration file and update it globally
func ReloadConfig() error {
	if configFile == "" {
		if fileExists("./mirrorbits.conf") {
			configFile = "./mirrorbits.conf"
		} else if fileExists("/etc/mirrorbits.conf") {
			configFile = "/etc/mirrorbits.conf"
		}
	}

	content, err := ioutil.ReadFile(configFile)
	if err != nil {
		fmt.Println("Configuration could not be found.\n\tUse -config <path>")
		os.Exit(-1)
	}

	if os.Getenv("DEBUG") != "" {
		fmt.Println("Reading configuration from", configFile)
	}

	c := defaultConfig

	// Overload the default configuration with the user's one
	err = yaml.Unmarshal(content, &c)
	if err != nil {
		return fmt.Errorf("%s in %s", err, configFile)
	}

	// Sanitize
	if c.WeightDistributionRange <= 0 {
		return fmt.Errorf("WeightDistributionRange must be > 0")
	}
	if !isInSlice(c.OutputMode, []string{"auto", "json", "redirect"}) {
		return fmt.Errorf("Config: outputMode can only be set to 'auto', 'json' or 'redirect'")
	}
	c.Repository = strings.TrimRight(c.Repository, "/")
	if c.RepositoryScanInterval < 0 {
		c.RepositoryScanInterval = 0
	}

	if config != nil &&
		(c.RedisAddress != config.RedisAddress ||
			c.RedisPassword != config.RedisPassword ||
			!testSentinelsEq(c.RedisSentinels, config.RedisSentinels)) {
		// TODO reload redis connections
		// Currently established connections will be updated only in case of disconnection
	}

	configMutex.Lock()
	config = &c
	configMutex.Unlock()
	return nil
}

// GetConfig returns a pointer to a configuration object
// FIXME reading from the pointer could cause a race!
func GetConfig() *configuration {
	configMutex.RLock()
	defer configMutex.RUnlock()

	if config == nil {
		panic("Configuration not loaded")
	}

	return config
}

func fileExists(filename string) bool {
	_, err := os.Stat(filename)
	return err == nil
}

func testSentinelsEq(a, b []sentinels) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i].Host != b[i].Host {
			return false
		}
	}

	return true
}
