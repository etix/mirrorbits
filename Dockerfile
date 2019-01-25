FROM golang:latest

LABEL maintainer="etix@l0cal.com"

ADD . /go/mirrorbits

RUN apt-get update -y && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y pkg-config zlib1g-dev protobuf-compiler libprotoc-dev rsync && \
    apt-get clean
RUN go get -u github.com/maxmind/geoipupdate2/cmd/geoipupdate && \
    go install -ldflags "-X main.defaultConfigFile=/etc/GeoIP.conf -X main.defaultDatabaseDirectory=/usr/share/GeoIP" github.com/maxmind/geoipupdate2/cmd/geoipupdate && \
    echo "AccountID 0\nLicenseKey 000000000000\nEditionIDs GeoLite2-City GeoLite2-Country GeoLite2-ASN" > /etc/GeoIP.conf && \
    mkdir /usr/share/GeoIP && \
    /go/bin/geoipupdate
RUN mkdir /srv/repo /var/log/mirrorbits && \
    cd /go/mirrorbits && \
    make install PREFIX=/usr
RUN cp /go/mirrorbits/contrib/docker/mirrorbits.conf /etc/mirrorbits.conf

ENTRYPOINT /usr/bin/mirrorbits daemon -config /etc/mirrorbits.conf

EXPOSE 8080
