# dora
BUILDTIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
VERSION := $(shell git rev-parse --short HEAD)

GOLDFLAGS += -X 'github.com/ethpandaops/dora/utils.BuildVersion="$(VERSION)"'
GOLDFLAGS += -X 'github.com/ethpandaops/dora/utils.Buildtime="$(BUILDTIME)"'
GOLDFLAGS += -X 'github.com/ethpandaops/dora/utils.BuildRelease="$(RELEASE)"'

.PHONY: all test clean

all: docs build

test: ensure-ui
	$(MAKE) -C ui-package test
	go test ./...

build: ensure-ui
	@echo version: $(VERSION)
	env CGO_ENABLED=1 go build -v -o bin/ -ldflags="-s -w $(GOLDFLAGS)" ./cmd/*

ensure-ui:
	if [ ! -f ui-package/dist/react-ui.js ]; then $(MAKE) build-ui; fi

build-ui:
	$(MAKE) -C ui-package install
	$(MAKE) -C ui-package build

clean:
	rm -f bin/*
	$(MAKE) -C ui-package clean

devnet:
	.hack/devnet/run.sh

devnet-run: devnet ensure-ui
	go run cmd/dora-explorer/main.go --config .hack/devnet/generated-dora-config.yaml

devnet-clean:
	.hack/devnet/cleanup.sh

docs:
	go install github.com/swaggo/swag/cmd/swag@v1.16.3 && swag init -g handler.go -d handlers/api --parseDependency -o handlers/docs