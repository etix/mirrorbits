Mirrorbits
===========

Mirrorbits is a geographic download redirector written in [Go](www.golang.org) for distributing files across a set of mirrors.

![mirrorbits_screenshot](https://cloud.githubusercontent.com/assets/38853/3501038/a61daefe-0611-11e4-92ac-db79a3c09e8a.png)

# Main Features

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

# Installation

```
$ go get github.com/etix/mirrorbits
$ go install -v github.com/etix/mirrorbits
```

# Documentation

Later.

# Is it production ready?

**Mostly!** Mirrorbits is already running in production at [VideoLAN](http://www.videolan.org) to distribute [VLC media player](http://www.videolan.org/vlc/) since April 2014 but the JSON API and the configuration file are still subject to change.

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
