package services

import (
	"fmt"
	"testing"
	"time"

	"github.com/ethpandaops/dora/types"
	"github.com/sirupsen/logrus"
)

func newTestReloader(initial []types.EndpointConfig, addClient func(*types.EndpointConfig) error) *endpointsReloader {
	logger := logrus.New()
	logger.SetLevel(logrus.PanicLevel)

	return newEndpointsReloader(logger, "beacon", "http://test/endpoints.yaml", time.Minute, initial, addClient)
}

func TestReconcileAddsNewEndpoints(t *testing.T) {
	added := []string{}
	reloader := newTestReloader(
		[]types.EndpointConfig{{Name: "node-1", Url: "http://node-1"}},
		func(endpoint *types.EndpointConfig) error {
			added = append(added, endpoint.Name)
			return nil
		},
	)

	fetched := []types.EndpointConfig{
		{Name: "node-1", Url: "http://node-1"},
		{Name: "node-2", Url: "http://node-2"},
	}

	reloader.reconcile(fetched)

	if len(added) != 1 || added[0] != "node-2" {
		t.Fatalf("expected only node-2 to be added, got %v", added)
	}

	// second reconcile with the same list must not re-add
	reloader.reconcile(fetched)

	if len(added) != 1 {
		t.Fatalf("expected no further adds on unchanged list, got %v", added)
	}
}

func TestReconcileRetriesFailedAdds(t *testing.T) {
	attempts := 0
	reloader := newTestReloader(nil, func(endpoint *types.EndpointConfig) error {
		attempts++
		if attempts == 1 {
			return fmt.Errorf("transient failure")
		}
		return nil
	})

	fetched := []types.EndpointConfig{{Name: "node-1", Url: "http://node-1"}}

	reloader.reconcile(fetched)
	reloader.reconcile(fetched)
	reloader.reconcile(fetched)

	if attempts != 2 {
		t.Fatalf("expected failed add to be retried once then succeed, got %v attempts", attempts)
	}
}

func TestReconcileChangedEndpointIsNotReadded(t *testing.T) {
	added := 0
	reloader := newTestReloader(
		[]types.EndpointConfig{{Name: "node-1", Url: "http://node-1"}},
		func(endpoint *types.EndpointConfig) error {
			added++
			return nil
		},
	)

	reloader.reconcile([]types.EndpointConfig{{Name: "node-1", Url: "http://node-1-changed"}})

	if added != 0 {
		t.Fatalf("changed endpoint must not be re-added, got %v adds", added)
	}

	if reloader.known["node-1"].Url != "http://node-1-changed" {
		t.Fatalf("changed endpoint config should be recorded to avoid repeated warnings")
	}
}

func TestReconcileRemovalAndReappearance(t *testing.T) {
	added := 0
	reloader := newTestReloader(
		[]types.EndpointConfig{
			{Name: "node-1", Url: "http://node-1"},
			{Name: "node-2", Url: "http://node-2"},
		},
		func(endpoint *types.EndpointConfig) error {
			added++
			return nil
		},
	)

	// node-2 disappears from the source
	reloader.reconcile([]types.EndpointConfig{{Name: "node-1", Url: "http://node-1"}})

	if !reloader.removedWarned["node-2"] {
		t.Fatalf("expected removal warning state for node-2")
	}

	// node-2 re-appears - its client was never torn down, so it must not be re-added
	reloader.reconcile([]types.EndpointConfig{
		{Name: "node-1", Url: "http://node-1"},
		{Name: "node-2", Url: "http://node-2"},
	})

	if added != 0 {
		t.Fatalf("re-appearing endpoint must not be re-added, got %v adds", added)
	}

	if reloader.removedWarned["node-2"] {
		t.Fatalf("removal warning state should be cleared on re-appearance")
	}
}

func TestReconcileIgnoresEmptyList(t *testing.T) {
	reloader := newTestReloader(
		[]types.EndpointConfig{{Name: "node-1", Url: "http://node-1"}},
		func(endpoint *types.EndpointConfig) error { return nil },
	)

	reloader.reconcile([]types.EndpointConfig{})

	if reloader.removedWarned["node-1"] {
		t.Fatalf("an empty endpoint list must be ignored, not treated as mass removal")
	}
}

func TestEndpointNameDefaultsToHostname(t *testing.T) {
	name := endpointName(&types.EndpointConfig{Url: "https://bn-node-1.example.com:5052"})
	if name != "bn-node-1.example.com" {
		t.Fatalf("expected hostname default, got %v", name)
	}
}

func TestEndpointsReloadInterval(t *testing.T) {
	if endpointsReloadInterval(0) != defaultEndpointsReloadInterval {
		t.Fatalf("zero should resolve to the default interval")
	}

	if endpointsReloadInterval(-1) != 0 {
		t.Fatalf("negative should disable the reloader")
	}

	if endpointsReloadInterval(time.Hour) != time.Hour {
		t.Fatalf("positive values should be used as-is")
	}
}
