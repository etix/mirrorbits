FROM golang:1.3

RUN mkdir -p /go/src/mirrorbits
WORKDIR /go/src/mirrorbits

RUN apt-get update -y && apt-get install -y zlib1g-dev pkg-config libgeoip-dev git
ENTRYPOINT ["go-wrapper", "run"]

COPY . /go/src/mirrorbits
RUN go-wrapper download
RUN go-wrapper install

