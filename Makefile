.PHONY: all build dev clean release test installdirs install uninstall install-service uninstall-service service-systemd regen-proto

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

GOPATH ?= $(HOME)/go

export PATH := $(GOPATH)/bin:$(PATH)

PKG_CONFIG ?= /usr/bin/pkg-config
SERVICEDIR_SYSTEMD ?= $(shell $(PKG_CONFIG) systemd --variable=systemdsystemunitdir)

all: build

regen-proto: rpc/rpc.proto
	@ if ! which protoc > /dev/null; then \
		echo "error: protoc not installed" >&2; \
		exit 1; \
	fi
	go install github.com/golang/protobuf/protoc-gen-go@v1.3.5 && \
	rm -f rpc/rpc.pb.go && \
	protoc -I rpc rpc/rpc.proto --go_out=plugins=grpc:rpc

build:
	GO111MODULE=on go build $(GOFLAGS) -o $(BINARY) .

dev:
	GO111MODULE=on go build $(GOFLAGSDEV) -o $(BINARY) .

clean:
	@echo Cleaning workspace...
	@rm -f $(BINARY)
	@rm -f contrib/init/systemd/mirrorbits.service
	@rm -dRf dist

release: $(TARBALL)

test:
	GO111MODULE=on go test $(GOFLAGS) ./...

installdirs:
	mkdir -p ${DESTDIR}${PREFIX}/{bin,share} ${DESTDIR}$(PREFIX)/share/mirrorbits

install: build installdirs install-service
# For the 'make install' to work with sudo it might be necessary to add
# the Go binary path to the 'secure_path' and add 'GOPATH' to 'env_keep'.
	@cp -vf $(BINARY) ${DESTDIR}${PREFIX}/bin/
	@cp -vf templates/* ${DESTDIR}$(PREFIX)/share/mirrorbits

uninstall: uninstall-service
	@rm -vf ${DESTDIR}${PREFIX}/bin/$(BINARY_NAME)
	@rm -vfr ${DESTDIR}$(PREFIX)/share/mirrorbits

ifeq (,${SERVICEDIR_SYSTEMD})
install-service:
uninstall-service:
else
install-service: service-systemd
	install -Dm644 contrib/init/systemd/mirrorbits.service ${DESTDIR}${SERVICEDIR_SYSTEMD}/mirrorbits.service

uninstall-service:
	@rm -vf ${DESTDIR}${SERVICEDIR_SYSTEMD}/mirrorbits.service

service-systemd:
	@sed "s|##PREFIX##|$(PREFIX)|" contrib/init/systemd/mirrorbits.service.in > contrib/init/systemd/mirrorbits.service
endif

$(TARBALL): build
	@echo Packaging release...
	@mkdir -p tmp/mirrorbits
	@cp -f $(BINARY) tmp/mirrorbits/
	@cp -r templates tmp/mirrorbits/
	@cp mirrorbits.conf tmp/mirrorbits/
	@mkdir -p dist/
	@tar -czf $@ -C tmp mirrorbits && echo release tarball has been created: $@
	@rm -rf tmp

