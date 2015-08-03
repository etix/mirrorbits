.PHONY: all build dev install clean release

VERSION := $(shell git describe --always --dirty --tags)
SHA := $(shell git rev-parse --short HEAD)
BRANCH := $(subst /,-,$(shell git rev-parse --abbrev-ref HEAD))
BUILD := $(SHA)-$(BRANCH)
PACKAGE := dist/mirrorbits-$(VERSION).tar.gz

GOFLAGS := -ldflags "-X main.VERSION $(VERSION) -X main.BUILD $(BUILD)"
GOFLAGSDEV := -race -ldflags "-X main.VERSION $(VERSION) -X main.BUILD $(BUILD) -X main.DEV -dev"

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

$(PACKAGE): build
	@echo Packaging release...
	@mkdir -p tmp/mirrorbits
	@cp -f bin/mirrorbits tmp/mirrorbits/
	@cp -r templates tmp/mirrorbits/
	@cp mirrorbits.conf tmp/mirrorbits/
	@mkdir -p dist/
	tar -cf $@ -C tmp mirrorbits;
	@rm -rf tmp

