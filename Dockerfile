FROM golang:latest

LABEL maintainer="etix@l0cal.com"

ADD . /go/src/github.com/etix/mirrorbits

RUN apt-get update -y && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y apt-utils && \
    apt-get install -y pkg-config zlib1g-dev rsync && \
    apt-get clean
RUN go get -u github.com/maxmind/geoipupdate2/cmd/geoipupdate && \
    go install -ldflags "-X main.defaultConfigFile=/etc/GeoIP.conf -X main.defaultDatabaseDirectory=/usr/share/GeoIP" github.com/maxmind/geoipupdate2/cmd/geoipupdate && \
    echo "AccountID 0\nLicenseKey 000000000000\nEditionIDs GeoLite2-City GeoLite2-Country GeoLite2-ASN" > /etc/GeoIP.conf && \
    mkdir /usr/share/GeoIP && \
    /go/bin/geoipupdate
RUN mkdir /srv/repo /var/log/mirrorbits && \
    cd /go/src/github.com/etix/mirrorbits && \
    make install PREFIX=/usr
RUN cp /go/src/github.com/etix/mirrorbits/contrib/docker/mirrorbits.conf /etc/mirrorbits.conf

ENTRYPOINT /bin/sh /go/src/github.com/etix/mirrorbits/contrib/docker/prepare.sh && /usr/bin/mirrorbits -config /etc/mirrorbits.conf -D

EXPOSE 8080
