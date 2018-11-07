.PHONY: all build dev install clean release vendor

VERSION := $(shell git describe --always --dirty --tags)
SHA := $(shell git rev-parse --short HEAD)
BRANCH := $(subst /,-,$(shell git rev-parse --abbrev-ref HEAD))
BUILD := $(SHA)-$(BRANCH)
BINARY_NAME := mirrorbits
BINARY := bin/$(BINARY_NAME)
TARBALL := dist/mirrorbits-$(VERSION).tar.gz
TEMPLATES := templates/
ifneq (${DESTDIR}$(PREFIX),)
TEMPLATES = ${DESTDIR}$(PREFIX)/share/mirrorbits
endif
PREFIX ?= /usr/local
PACKAGE = github.com/etix/mirrorbits

LDFLAGS := -X $(PACKAGE)/core.VERSION=$(VERSION) -X $(PACKAGE)/core.BUILD=$(BUILD) -X $(PACKAGE)/config.TEMPLATES_PATH=${TEMPLATES}
GOFLAGS := -ldflags "$(LDFLAGS)"
GOFLAGSDEV := -race -ldflags "$(LDFLAGS) -X $(PACKAGE)/core.DEV=-dev"

export PATH := ${GOPATH}/bin:$(PATH)

all: build

vendor:
	go get github.com/kardianos/govendor
	govendor sync ${PACKAGE}

build: vendor
	go build $(GOFLAGS) -o $(BINARY) .

dev: vendor
	go build $(GOFLAGSDEV) -o $(BINARY) .

clean:
	@echo Cleaning workspace...
	@rm -f $(BINARY)
	@rm -dRf dist

release: $(TARBALL)

test:
	@govendor test $(GOFLAGS) -v -cover +local

installdirs:
	mkdir -p ${DESTDIR}${PREFIX}/{bin,share} ${TEMPLATES}

install: build installdirs
	@cp -vf $(BINARY) ${DESTDIR}${PREFIX}/bin/
	@cp -vf templates/* ${TEMPLATES}

$(TARBALL): build
	@echo Packaging release...
	@mkdir -p tmp/mirrorbits
	@cp -f $(BINARY) tmp/mirrorbits/
	@cp -r templates tmp/mirrorbits/
	@cp mirrorbits.conf tmp/mirrorbits/
	@mkdir -p dist/
	@tar -czf $@ -C tmp mirrorbits && echo release tarball has been created: $@
	@rm -rf tmp

