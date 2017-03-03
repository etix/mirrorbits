.PHONY: all build dev install clean release vendor

VERSION := $(shell git describe --always --dirty --tags)
SHA := $(shell git rev-parse --short HEAD)
BRANCH := $(subst /,-,$(shell git rev-parse --abbrev-ref HEAD))
BUILD := $(SHA)-$(BRANCH)
TARBALL := dist/mirrorbits-$(VERSION).tar.gz

PACKAGE = github.com/etix/mirrorbits

GOFLAGS := -ldflags "-X $(PACKAGE)/core.VERSION=$(VERSION) -X $(PACKAGE)/core.BUILD=$(BUILD)"
GOFLAGSDEV := -race -ldflags "-X $(PACKAGE)/core.VERSION=$(VERSION) -X $(PACKAGE)/core.BUILD=$(BUILD) -X $(PACKAGE)/core.DEV=-dev"

all: build

vendor:
	go get github.com/kardianos/govendor
	govendor sync ${PACKAGE}

build: vendor
	go build $(GOFLAGS) -o bin/mirrorbits .

dev: vendor
	go build $(GOFLAGSDEV) -o bin/mirrorbits .

install: vendor
	go install -v $(GOFLAGS) .

clean:
	@echo Cleaning workspace...
	@rm -f bin/mirrorbits
	@rm -dRf dist

release: $(TARBALL)

test:
	@govendor test $(GOFLAGS) -v -cover +local

$(TARBALL): build
	@echo Packaging release...
	@mkdir -p tmp/mirrorbits
	@cp -f bin/mirrorbits tmp/mirrorbits/
	@cp -r templates tmp/mirrorbits/
	@cp mirrorbits.conf tmp/mirrorbits/
	@mkdir -p dist/
	@tar -czf $@ -C tmp mirrorbits && echo release tarball has been created: $@
	@rm -rf tmp

