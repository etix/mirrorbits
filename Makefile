.PHONY: all build dev install clean release

VERSION := $(shell git describe --always --dirty --tags)
SHA := $(shell git rev-parse --short HEAD)
BRANCH := $(subst /,-,$(shell git rev-parse --abbrev-ref HEAD))
BUILD := $(SHA)-$(BRANCH)
PACKAGE := dist/mirrorbits-$(VERSION).tar.gz

SYMBOLSPREFIX = github.com/etix/mirrorbits/

GOFLAGS := -ldflags "-X $(SYMBOLSPREFIX)core.VERSION=$(VERSION) -X $(SYMBOLSPREFIX)core.BUILD=$(BUILD)"
GOFLAGSDEV := -race -ldflags "-X $(SYMBOLSPREFIX)core.VERSION=$(VERSION) -X $(SYMBOLSPREFIX)core.BUILD=$(BUILD) -X $(SYMBOLSPREFIX)core.DEV=-dev"

all: build

build:
	go build $(GOFLAGS) -o bin/mirrorbits .

dev:
	go build $(GOFLAGSDEV) -o bin/mirrorbits .

install:
	go install -v $(GOFLAGS) .

clean:
	@echo Cleaning workspace...
	@rm -dRf bin
	@rm -dRf dist

release: $(PACKAGE)

test:
	@go test $(GOFLAGS) -v -cover ./...

$(PACKAGE): build
	@echo Packaging release...
	@mkdir -p tmp/mirrorbits
	@cp -f bin/mirrorbits tmp/mirrorbits/
	@cp -r templates tmp/mirrorbits/
	@cp mirrorbits.conf tmp/mirrorbits/
	@mkdir -p dist/
	@tar -czf $@ -C tmp mirrorbits && echo release tarball has been created: $@
	@rm -rf tmp

