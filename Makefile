VERSION := $(shell git rev-parse --short HEAD)
BUILDTIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')

GOLDFLAGS += -X 'github.com/pk910/light-beaconchain-explorer/utils.BuildVersion=$(VERSION)'
GOLDFLAGS += -X 'github.com/pk910/light-beaconchain-explorer/utils.Buildtime=$(BUILDTIME)'
GOFLAGS = -ldflags "$(GOLDFLAGS)"

run: build

build:
	go install $(GOFLAGS) ./cmd/explorer
