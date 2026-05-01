package rpc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/ethpandaops/go-eth2-client/spec"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/sirupsen/logrus"
)

func TestGetBlockBodyByBlockrootJSONHeze(t *testing.T) {
	fixturePath := filepath.Join("testdata", "slot32-heze.json")
	payload, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Fatalf("expected Accept application/json, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	bc, err := NewBeaconClient("test", srv.URL, nil, nil, false, logrus.New())
	if err != nil {
		t.Fatalf("new beacon client: %v", err)
	}

	block, err := bc.getBlockBodyByBlockrootJSON(context.Background(), phase0.Root{})
	if err != nil {
		t.Fatalf("get block body by root json: %v", err)
	}
	if block == nil {
		t.Fatalf("expected block, got nil")
	}
	if block.Version != spec.DataVersionHeze {
		t.Fatalf("expected heze version, got %v", block.Version)
	}
	if block.Heze == nil {
		t.Fatalf("expected heze block payload")
	}
}

func TestIsHezeSignedBlockContentsDecodeError(t *testing.T) {
	err := errors.New("failed to decode heze signed block contents\nMessage.Body.ProposerSlashings:o: incorrect offset: first offset 396 does not match expected 392")
	if !isHezeSignedBlockContentsDecodeError(err) {
		t.Fatalf("expected matcher to detect Heze signed block contents decode failure")
	}
}
