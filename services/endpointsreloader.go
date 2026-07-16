package services

import (
	"context"
	"net/url"
	"reflect"
	"time"

	"github.com/ethpandaops/dora/types"
	"github.com/ethpandaops/dora/utils"
	"github.com/sirupsen/logrus"
)

const defaultEndpointsReloadInterval = time.Hour

// endpointsReloader periodically re-fetches an endpointsUrl source and hot-adds
// endpoints that appear there, so new nodes show up without a restart.
// Changed or removed endpoints only log a warning - applying those needs a restart.
type endpointsReloader struct {
	logger    logrus.FieldLogger
	kind      string
	sourceURL string
	interval  time.Duration
	addClient func(*types.EndpointConfig) error

	known         map[string]types.EndpointConfig
	removedWarned map[string]bool
}

func (cs *ChainService) startEndpointsReloader() {
	if sourceURL := utils.Config.BeaconApi.EndpointsURL; sourceURL != "" {
		if interval := endpointsReloadInterval(utils.Config.BeaconApi.EndpointsReloadInterval); interval > 0 {
			reloader := newEndpointsReloader(cs.logger, "beacon", sourceURL, interval, utils.Config.BeaconApi.Endpoints, cs.addConsensusClient)
			go reloader.run(cs.ctx)
		}
	}

	if sourceURL := utils.Config.ExecutionApi.EndpointsURL; sourceURL != "" {
		if interval := endpointsReloadInterval(utils.Config.ExecutionApi.EndpointsReloadInterval); interval > 0 {
			reloader := newEndpointsReloader(cs.logger, "execution", sourceURL, interval, utils.Config.ExecutionApi.Endpoints, cs.addExecutionClient)
			go reloader.run(cs.ctx)
		}
	}
}

func newEndpointsReloader(logger logrus.FieldLogger, kind string, sourceURL string, interval time.Duration, initial []types.EndpointConfig, addClient func(*types.EndpointConfig) error) *endpointsReloader {
	// endpoints already wired (or attempted) at startup
	known := make(map[string]types.EndpointConfig, len(initial))
	for _, endpoint := range initial {
		known[endpointName(&endpoint)] = endpoint
	}

	return &endpointsReloader{
		logger:        logger.WithField("service", kind+"-endpoints-reloader"),
		kind:          kind,
		sourceURL:     sourceURL,
		interval:      interval,
		addClient:     addClient,
		known:         known,
		removedWarned: map[string]bool{},
	}
}

// endpointsReloadInterval resolves the configured interval (0 = default, negative = disabled).
func endpointsReloadInterval(configured time.Duration) time.Duration {
	if configured == 0 {
		return defaultEndpointsReloadInterval
	}

	if configured < 0 {
		return 0
	}

	return configured
}

// endpointName mirrors the name defaulting applied on initial config load.
func endpointName(endpoint *types.EndpointConfig) string {
	if endpoint.Name != "" {
		return endpoint.Name
	}

	if urlObj, err := url.Parse(endpoint.Url); err == nil {
		return urlObj.Hostname()
	}

	return endpoint.Url
}

func (r *endpointsReloader) run(ctx context.Context) {
	r.logger.Infof("watching %v for new endpoints (interval: %v)", r.sourceURL, r.interval)

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(r.interval):
		}

		endpoints, err := utils.LoadEndpointsFromURL(r.sourceURL)
		if err != nil {
			r.logger.WithError(err).Errorf("failed to reload endpoints from %v", r.sourceURL)
			continue
		}

		r.reconcile(endpoints)
	}
}

// reconcile diffs the fetched endpoints against the known set and hot-adds new ones.
func (r *endpointsReloader) reconcile(endpoints []types.EndpointConfig) {
	if len(endpoints) == 0 {
		r.logger.Warnf("endpoint source %v returned no endpoints, ignoring", r.sourceURL)
		return
	}

	seen := make(map[string]bool, len(endpoints))

	for _, endpoint := range endpoints {
		name := endpointName(&endpoint)
		seen[name] = true

		if knownEndpoint, ok := r.known[name]; ok {
			if r.removedWarned[name] {
				delete(r.removedWarned, name)
				r.logger.Infof("endpoint '%v' re-appeared in %v, client is still active", name, r.sourceURL)
			}

			if !reflect.DeepEqual(knownEndpoint, endpoint) {
				r.logger.Warnf("endpoint '%v' changed in %v, applying the change requires a restart", name, r.sourceURL)
				r.known[name] = endpoint
			}

			continue
		}

		if err := r.addClient(&endpoint); err != nil {
			r.logger.WithError(err).Errorf("failed to add new %v endpoint '%v'", r.kind, name)
			continue
		}

		r.known[name] = endpoint
		r.logger.Infof("added new %v endpoint '%v' (%v)", r.kind, name, endpoint.Url)
	}

	// clients are never torn down at runtime, so removals only get a warning
	for name := range r.known {
		if !seen[name] && !r.removedWarned[name] {
			r.logger.Warnf("endpoint '%v' removed from %v, client stays active until restart", name, r.sourceURL)
			r.removedWarned[name] = true
		}
	}
}
