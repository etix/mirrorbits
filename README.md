[![Build Status](https://travis-ci.org/etix/mirrorbits.svg?branch=master)](https://travis-ci.org/etix/mirrorbits)
[![Go Report Card](https://goreportcard.com/badge/github.com/etix/mirrorbits)](https://goreportcard.com/report/github.com/etix/mirrorbits)

Mirrorbits
===========

Mirrorbits is a geographical download redirector written in [Go](https://golang.org) for distributing files efficiently across a set of mirrors. It offers a simple and economic way to create a Content Delivery Network layer using a pure software stack. It is primarily designed for the distribution of large-scale Open-Source projects with a lot of traffic.

![mirrorbits_screenshot](https://cloud.githubusercontent.com/assets/38853/3636687/ab6bba38-0fd8-11e4-9d69-01543ed2531a.png)

## Main Features

* Blazing fast, can reach 8K QPS on a single laptop
* Easy to deploy and maintain, everything is packed in a single binary
* Automatic synchronization with the mirrors over **rsync** or **FTP**
* Response can be either JSON or HTTP redirect
* Support partial repositories
* Complete checksum / size control
* Realtime monitoring and reports
* Disable misbehaving mirrors without human intervention
* Realtime decision making based on location, AS number and defined rules
* Smart load-balancing over multiple mirrors in the same area to avoid hotspots
* Ability to adjust the weight of each mirror
* Limit access to a country, region or ASN for any mirror
* Clustering (multiple mirrorbits instances)
* High-availability using redis-sentinel
* Realtime statistics per file / mirror / date
* Realtime reconfiguration
* Seamless binary upgrade (aka zero downtime upgrade)
* [Mirmon](http://www.staff.science.uu.nl/~penni101/mirmon/) support
* Full **IPv6** support
* more...

## Is it production ready?

**Yes!** Mirrorbits is already running in production at
 * [VideoLAN](http://www.videolan.org) to distribute [VLC media player](http://www.videolan.org/vlc/) since April 2014
 * [Popcorn Time](https://popcorntime.io)
 * [SuperRepo](https://superrepo.org)
 * [Kodi](http://kodi.tv) (aka XBMC)
 * [OSMC](https://osmc.tv)
 * [LineageOS](http://lineageos.org) (previously CyanogenMod)
 * [Chaos Computer Club](https://media.ccc.de) (media distribution)
 * [CarbonROM](https://carbonrom.org)
 * [Endless OS](https://endlessos.com/)

Yet some things might change before the 1.0 release. If you intend to deploy Mirrorbits in a production system it is advised to notify the author first so we can help you to make any transition as seamless as possible!

# Quick start

## Prerequisites

* Go 1.7 or later
* Redis 3.2 or later (with [persistence](https://redis.io/topics/persistence) enabled)
* GeoIP2 databases from [Maxmind](https://dev.maxmind.com/geoip/geoip2/geolite2/) (preferably updated regularly)

:warning: **GeoIP-legacy is not supported anymore, please use the new GeoIP2 mmdb databases!**

**Optional:**

* redis-sentinel (for high-availability support)

## Installation

You can either get a [prebuilt version](https://github.com/etix/mirrorbits/releases) or choose to build it yourself.

### Manual build

```
$ go get -u github.com/etix/mirrorbits
$ cd $GOPATH/src/github.com/etix/mirrorbits
$ make install
```
The resulting executable should now be in your *$GOPATH/bin* directory.

If you plan to use the web UI be sure to copy the [templates](templates) into your system (usually in /usr/share/mirrorbits).

## Configuration

A sample configuration file can be found [here](mirrorbits.conf).

Option | description
------ | -----------
Repository | Path to your own copy of the repository
Templates | Path containing the templates
OutputMode | auto: based on the *Accept* header content<br>redirect: do an HTTP redirect to the destination<br>json: return a JSON formatted document (also known as API mode)
ListenAddress | Local address and port to bind
Gzip | Use gzip compression for the JSON responses
RedisAddress | Address and port of the Redis database
RedisPassword | Password to access the Redis database
RedisDB | Database index
RedisSentinelMasterName | Name of the redis-sentinel cluster
RedisSentinels | List of redis-sentinel hosts
LogDir | Path to the directory where to save log files
TraceFileLocation | Relative path to a trace file (from the repository root) containing a Unix timestamp regularly updated
GeoipDatabasePath | Path to the geoip databases
ConcurrentSync | Maximum number of server sync (rsync/ftp) do to simultaneously
ScanInterval | Interval between rsync/ftp synchronizations (in minutes)
CheckInterval | Interval between mirrors health's checks (in minutes)
RepositoryScanInterval | Interval between scans of the local repository (in minutes, 0 to disable)
Hashes | List of file hashes to computes (SHA1, SHA256, MD5)
DisallowRedirects | Disable any mirror trying to do an HTTP redirect
WeightDistributionRange | Multiplier of the distance to the first mirror to find other possible mirrors in order to distribute the load
DisableOnMissingFile | Disable a mirror if an advertised file on rsync/ftp appears to be missing on HTTP
MaxLinkHeaders | Amount of backup mirror locations returned in HTTP headers
Fallbacks | A list of possible mirrors to use as fallback if a request fails or if the database is unreachable. **These mirrors are not tracked by mirrorbits.** It is assumed they have all the files available in the local repository.
LocalJSPath | A local path or URL containing the JavaScript used by the templates. If this is not set (the default), the JavaScript will just be loaded from the usual CDNs. See also `contrib/localjs/fetchfiles.sh`.

## Running

Mirrorbits is a self-contained application and can act, at the same time, as the server and the cli.

To run the server:
```
mirrorbits -D
```
Additional options can be found with ```mirrorbits -help```.

To run the cli:
```
mirrorbits help
```

Add a mirror:
```
mirrorbits add -ftp="ftp://ftp.mirrors.example/myproject/" -http="http://ftp.mirrors.example/myproject/" mirrors.example
```

Enable the mirror:
```
mirrorbits enable mirrors.example
```

### Realtime file availability

By appending `?mirrorlist` to any file served by mirrorbits, you'll be able to get some useful realtime informations about the given file. You can see a [live example here](https://get.videolan.org/vlc/2.2.4/win32/vlc-2.2.4-win32.exe?mirrorlist).

### Realtime mirrors statistics

Mirror statistics are available by querying mirrorbits with the `?mirrorstats` argument. You can see a [live example here](https://get.videolan.org/?mirrorstats).

## Clustering / High availability

Multiple instances of mirrorbits can be started simultanously on different servers, discovery of other nodes should be automatic as long as all the instances are connected to the same redis server. In addition to the clustering it is advised to use redis-sentinel to monitor the database and gracefuly handle failover.

## Upgrading

Mirrorbits has a mode called *seamless binary upgrade* to upgrade the server executable at runtime without service disruption. Once the binary has been replaced on the filesystem just issue the following command in the cli:
```
mirrorbits upgrade
```

## Considerations

* When configured in redirect mode, Mirrorbits can easily serve client requests directly but it is usually recommended to set it behind a reverse proxy like nginx. In this case take care to pass the IP address of the client within a X-Forwarded-For header:
```
proxy_set_header X-Forwarded-For $remote_addr;
```
* It is advised to never cache requests intended for Mirrorbits since each request is supposed to be unique, caching the result might have unexpected consequences.

# We're social!

The best place to discuss about mirrorbits is to join the #VideoLAN IRC channel on Freenode.  
For the latest news, you can follow [@mirrorbits](http://twitter.com/mirrorbits) on Twitter.

# License MIT

> Permission is hereby granted, free of charge, to any person obtaining a copy
> of this software and associated documentation files (the "Software"), to deal
> in the Software without restriction, including without limitation the rights
> to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
> copies of the Software, and to permit persons to whom the Software is
> furnished to do so, subject to the following conditions:
>
> The above copyright notice and this permission notice shall be included in
> all copies or substantial portions of the Software.
>
> THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
> IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
> FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
> AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
> LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
> OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
> THE SOFTWARE.
