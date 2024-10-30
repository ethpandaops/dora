# dora
BUILDTIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
VERSION := $(shell git rev-parse --short HEAD)

GOLDFLAGS += -X 'github.com/ethpandaops/dora/utils.BuildVersion="$(VERSION)"'
GOLDFLAGS += -X 'github.com/ethpandaops/dora/utils.Buildtime="$(BUILDTIME)"'
GOLDFLAGS += -X 'github.com/ethpandaops/dora/utils.BuildRelease="$(RELEASE)"'

.PHONY: all test clean

all: test build

test:
	$(MAKE) -C ui-package test
	if [ ! -f ui-package/dist/react-ui.js ]; then $(MAKE) build-ui; fi
	go test ./...

build:
	@echo version: $(VERSION)
	if [ ! -f ui-package/dist/react-ui.js ]; then $(MAKE) build-ui; fi
	env CGO_ENABLED=1 go build -v -o bin/ -ldflags="-s -w $(GOLDFLAGS)" ./cmd/*

build-ui:
	$(MAKE) -C ui-package install
	$(MAKE) -C ui-package build

clean:
	rm -f bin/*
	$(MAKE) -C ui-package clean

devnet:
	.hack/devnet/run.sh

devnet-run: devnet
	go run cmd/dora-explorer/main.go --config .hack/devnet/generated-dora-config.yaml

devnet-clean:
	.hack/devnet/cleanup.sh