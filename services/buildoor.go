package services

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/utils"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/sirupsen/logrus"
)

var logger_buildoor = logrus.StandardLogger().WithField("module", "buildoor_inventory")

type BuildoorInventory struct {
	ctx             context.Context
	beaconIndexer   *beacon.Indexer
	overviewUrl     string
	instanceUrls    []string
	refreshInterval time.Duration

	entriesMutex sync.RWMutex
	entries      map[uint64]*buildoorEntry

	updaterRunning bool
}

type buildoorEntry struct {
	name string
	url  string
}

type buildoorHost struct {
	ID    int    `json:"id"`
	URL   string `json:"url"`
	Label string `json:"label"`
}

type buildoorHostsResponse struct {
	Hosts []buildoorHost `json:"hosts"`
}

type buildoorInstanceOverview struct {
	BuilderPubkey string `json:"builder_pubkey"`
	BuilderIndex  uint64 `json:"builder_index"`
}

func NewBuildoorInventory(ctx context.Context, beaconIndexer *beacon.Indexer) *BuildoorInventory {
	return &BuildoorInventory{
		ctx:           ctx,
		beaconIndexer: beaconIndexer,
		entries:       map[uint64]*buildoorEntry{},
	}
}

func (b *BuildoorInventory) StartUpdater() {
	if b.updaterRunning {
		return
	}

	for _, raw := range utils.Config.Frontend.BuildoorUrls {
		trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
		if trimmed != "" {
			b.instanceUrls = append(b.instanceUrls, trimmed)
		}
	}
	b.overviewUrl = strings.TrimRight(utils.Config.Frontend.BuildoorOverviewUrl, "/")

	if len(b.instanceUrls) == 0 && b.overviewUrl == "" {
		return
	}

	b.refreshInterval = utils.Config.Frontend.BuildoorRefreshInterval
	if b.refreshInterval <= 0 {
		b.refreshInterval = 5 * time.Minute
	}

	b.updaterRunning = true
	go b.runUpdaterLoop()
}

func (b *BuildoorInventory) runUpdaterLoop() {
	defer utils.HandleSubroutinePanic("BuildoorInventory.runUpdaterLoop", b.runUpdaterLoop)

	for {
		if err := b.refresh(); err != nil {
			logger_buildoor.WithError(err).Warn("buildoor inventory refresh failed")
		}

		select {
		case <-b.ctx.Done():
			return
		case <-time.After(b.refreshInterval):
		}
	}
}

func (b *BuildoorInventory) refresh() error {
	ctx, cancel := context.WithTimeout(b.ctx, 30*time.Second)
	defer cancel()

	client := &http.Client{Timeout: 10 * time.Second}

	instances, err := b.resolveInstances(ctx, client)
	if err != nil {
		return fmt.Errorf("resolve instances: %w", err)
	}

	newEntries := map[uint64]*buildoorEntry{}
	resolvedCount := 0
	unresolvedCount := 0

	for _, url := range instances {
		overview, err := b.fetchInstance(ctx, client, url)
		if err != nil {
			logger_buildoor.WithError(err).WithField("buildoor", url).Debug("skipping buildoor instance (overview fetch failed)")
			continue
		}

		pubkey, err := decodePubkey(overview.BuilderPubkey)
		if err != nil {
			logger_buildoor.WithError(err).WithField("buildoor", url).Debug("skipping buildoor instance (bad pubkey)")
			continue
		}

		flaggedIdx, found := b.beaconIndexer.GetValidatorIndexByPubkey(pubkey)
		if !found {
			unresolvedCount++
			continue
		}

		rawIdx := uint64(flaggedIdx) &^ beacon.BuilderIndexFlag
		newEntries[rawIdx] = &buildoorEntry{
			name: deriveBuilderName(url),
			url:  url,
		}
		resolvedCount++
	}

	b.entriesMutex.Lock()
	b.entries = newEntries
	b.entriesMutex.Unlock()

	logger_buildoor.Infof("buildoor inventory refreshed: %d resolved, %d unresolved", resolvedCount, unresolvedCount)
	return nil
}

func (b *BuildoorInventory) resolveInstances(ctx context.Context, client *http.Client) ([]string, error) {
	if len(b.instanceUrls) > 0 {
		return b.instanceUrls, nil
	}
	if b.overviewUrl == "" {
		return nil, nil
	}

	hosts, err := b.fetchHosts(ctx, client)
	if err != nil {
		return nil, err
	}

	urls := make([]string, 0, len(hosts.Hosts))
	for _, h := range hosts.Hosts {
		if h.URL == "" {
			continue
		}
		urls = append(urls, strings.TrimRight(h.URL, "/"))
	}
	return urls, nil
}

func (b *BuildoorInventory) fetchHosts(ctx context.Context, client *http.Client) (*buildoorHostsResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.overviewUrl+"/api/overview/hosts", nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	out := &buildoorHostsResponse{}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return nil, err
	}
	return out, nil
}

func (b *BuildoorInventory) fetchInstance(ctx context.Context, client *http.Client, url string) (*buildoorInstanceOverview, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/api/buildoor/overview", nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	out := &buildoorInstanceOverview{}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return nil, err
	}
	return out, nil
}

func (b *BuildoorInventory) GetBuilderName(builderIndex uint64) string {
	if b == nil {
		return ""
	}
	b.entriesMutex.RLock()
	defer b.entriesMutex.RUnlock()
	if entry := b.entries[builderIndex]; entry != nil {
		return entry.name
	}
	return ""
}

func (b *BuildoorInventory) GetBuilderURL(builderIndex uint64) string {
	if b == nil {
		return ""
	}
	b.entriesMutex.RLock()
	defer b.entriesMutex.RUnlock()
	if entry := b.entries[builderIndex]; entry != nil {
		return entry.url
	}
	return ""
}

func decodePubkey(s string) (phase0.BLSPubKey, error) {
	var pk phase0.BLSPubKey
	s = strings.TrimPrefix(s, "0x")
	if len(s) != 96 {
		return pk, fmt.Errorf("pubkey hex length %d, want 96", len(s))
	}
	if _, err := hex.Decode(pk[:], []byte(s)); err != nil {
		return pk, err
	}
	return pk, nil
}

func deriveBuilderName(rawUrl string) string {
	host := rawUrl
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimRight(host, "/")

	if i := strings.Index(host, "."); i >= 0 {
		host = host[:i]
	}
	if stripped := strings.TrimPrefix(host, "api-buildoor-"); stripped != host {
		return stripped
	}
	return host
}
