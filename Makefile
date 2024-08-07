# dora
BUILDTIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
VERSION := $(shell git rev-parse --short HEAD)

GOLDFLAGS += -X 'github.com/ethpandaops/dora/utils.BuildVersion="$(VERSION)"'
GOLDFLAGS += -X 'github.com/ethpandaops/dora/utils.Buildtime="$(BUILDTIME)"'
GOLDFLAGS += -X 'github.com/ethpandaops/dora/utils.BuildRelease="$(RELEASE)"'

.PHONY: all test clean

all: test build

test:
	go test ./...

build:
	@echo version: $(VERSION)
	env CGO_ENABLED=1 go build -v -o bin/ -ldflags="-s -w $(GOLDFLAGS)" ./cmd/*

clean:
	rm -f bin/*

devnet:
	.hack/devnet/run.sh

devnetdd:
	.hack/devnet/run_dd.sh

devnet-run: devnet
	go run cmd/dora-explorer/main.go --config .hack/devnet/generated-dora-config.yaml

devnetdd-run: devnetdd
	go run cmd/dora-explorer/main.go --config .hack/devnet/generated-dora-config.yaml


devnet-clean:
	.hack/devnet/cleanup.sh
