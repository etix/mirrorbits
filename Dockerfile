FROM golang:latest

MAINTAINER etix@l0cal.com

ADD . /go/src/github.com/etix/mirrorbits

RUN apt-get update -y && apt-get install -y apt-utils
RUN apt-get install -y pkg-config zlib1g-dev libgeoip-dev rsync
RUN mkdir /var/log/mirrorbits && mkdir /srv/repo && touch /srv/repo/hello
RUN /bin/bash /go/src/github.com/etix/mirrorbits/contrib/geoip/geoip-lite-update
RUN cd /go/src/github.com/etix/mirrorbits && go get -d ./...
RUN cd /go/src/github.com/etix/mirrorbits && make
RUN ln -s /go/src/github.com/etix/mirrorbits/bin/mirrorbits /usr/bin/mirrorbits && ln -s /go/src/github.com/etix/mirrorbits/contrib/docker/mirrorbits.conf /etc/mirrorbits.conf

ENTRYPOINT /bin/sh /go/src/github.com/etix/mirrorbits/contrib/docker/prepare.sh && /usr/bin/mirrorbits -config /etc/mirrorbits.conf -D

EXPOSE 8080
