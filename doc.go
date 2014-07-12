// Copyright (c) 2014 Ludovic Fauvet
// Licensed under the MIT license

// Mirrorbits is a geographic download redirector for distributing files efficiently across a set of mirrors.
//
// Prerequisites
//
// Before diving into the install section ensures you have:
//  - Redis 2.8 (or later)
//  - libgeoip
//  - a recent geoip database (see contrib/geoip/)
//
// Installation
//
// You can now proceed to the installation by downloading a prebuilt release on
// https://github.com/etix/mirrorbits/releases or by building it yourself:
//  go get github.com/etix/mirrorbits
//  go install -v github.com/etix/mirrorbits
// If you plan to use the web UI be sure to install the templates found on
// https://github.com/etix/mirrorbits/tree/master/templates into your system (usually in /usr/share/mirrorbits).
//
// Configuration
//
// A sample configuration file can be found in the git repository:
// https://github.com/etix/mirrorbits/blob/master/mirrorbits.conf
//
// Running
//
// Mirrorbits is a self-contained application and is, at the same time, the server and the cli.
//
// To run the server:
//  mirrorbits -D
// To run the cli:
//  mirrorbits help
//
// Upgrading
//
// Mirrorbits has a mode called seamless binary upgrade to upgrade the server executable at runtime
// without service disruption. Once the binary has been replaced just issue the following
// command in the cli:
//  mirrorbits upgrade
//
// For more informations visit the official page:
// https://github.com/etix/mirrorbits/
package main
