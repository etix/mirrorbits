Mirrorbits
===========

Mirrorbits is a geographical download redirector written in [Go](www.golang.org) for distributing files efficiently across a set of mirrors. It offers a simple and economic way to create a full Content Delivery Network layer using a pure software stack. It is primarily designed for the distribution of large-scale Open-Source projects with a lot of traffic.

![mirrorbits_screenshot](https://cloud.githubusercontent.com/assets/38853/3636687/ab6bba38-0fd8-11e4-9d69-01543ed2531a.png)

## Main Features

* Blazing fast, can reach 8K req/s on a single laptop
* Easy to deploy and maintain, everything is packed in a single binary
* Automatic synchronization over **rsync** or **FTP**
* Response can be either JSON or HTTP redirect
* Support partial repositories
* Complete checksum / size control
* Realtime monitoring and reports
* Disable misbehaving mirrors without human intervention
* Realtime decision making based on location, AS number and defined rules
* Smart load-balancing over multiple mirrors in the same area to avoid hotspots
* Ability to adjust the weight of each mirror
* Limit access to a country, region or ASN for any mirror
* Realtime statistics per file / mirror / date
* Realtime reconfiguration
* Seamless binary upgrade (aka zero downtime upgrade)
* Full **IPv6** support
* more...

## Is it production ready?

**Almost!** Mirrorbits is already running in production at [VideoLAN](http://www.videolan.org) to distribute [VLC media player](http://www.videolan.org/vlc/) since April 2014. Yet some things might change before the 1.0 release, notably the response of a JSON request and few configuration items. If you intend to deploy Mirrorbits in a production system it is strongly advised to contact the author first!

# Quick start

## Prerequisites

* Redis 2.8.12 (or later)
* libgeoip
* a recent geoip database (see contrib/geoip/)

**Optional:**

* redis-sentinel (for high-availability support)

## Installation

You can either get a [prebuilt version](https://github.com/etix/mirrorbits/releases) or choose to build it yourself.

### Manual build

```
$ go get github.com/etix/mirrorbits
$ go install -v github.com/etix/mirrorbits
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
LogDir | Path to the directory where to save log files
GeoipDatabasePath | Path to the geoip databases
ConcurrentSync | Maximum number of server sync (rsync/ftp) do to simultaneously
DisallowRedirects | Disable any mirror trying to do an HTTP redirect
WeightDistributionRange | Multiplier of the distance to the first mirror to find other possible mirrors in order to distribute the load
DisableOnMissingFile | Disable a mirror if an advertised file on rsync/ftp appears to be missing on HTTP
Fallbacks | A list of possible mirrors to use as fallback if a request fails or if the database is unreachable. **These mirrors are not tracked by mirrorbits.** It is assumed they have all the files available in the local repository.

## Running

Mirrorbits is a self-contained application and can act, at the same time, as the server and the cli.

To run the server:
```
mirrorbits -D
```
Additionnal options can be found with ```mirrobits -help```.

To run the cli:
```
mirrorbits help
```

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
* Having multiple instances of Mirrorbits sharing the same database is not yet (officially) supported, therefore don't do it in production.

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
