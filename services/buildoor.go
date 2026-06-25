package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ethpandaops/dora/utils"
	"github.com/sirupsen/logrus"
)

var logger_buildoor = logrus.StandardLogger().WithField("module", "buildoor_inventory")

type BuildoorInventory struct {
	ctx             context.Context
	overviewUrl     string
	instanceHosts   []buildoorHost
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
	BuilderIndex uint64 `json:"builder_index"`
	IsRegistered bool   `json:"is_registered"`
}

func NewBuildoorInventory(ctx context.Context) *BuildoorInventory {
	return &BuildoorInventory{
		ctx:     ctx,
		entries: map[uint64]*buildoorEntry{},
	}
}

func (b *BuildoorInventory) StartUpdater() {
	if b.updaterRunning {
		return
	}

	for _, raw := range utils.Config.Frontend.BuildoorUrls {
		// each entry is either "url" or "label|url" where label is the service name
		entry := strings.TrimSpace(raw)
		label := ""
		if i := strings.Index(entry, "|"); i >= 0 {
			label = strings.TrimSpace(entry[:i])
			entry = strings.TrimSpace(entry[i+1:])
		}
		url := strings.TrimRight(entry, "/")
		if url != "" {
			b.instanceHosts = append(b.instanceHosts, buildoorHost{URL: url, Label: label})
		}
	}
	b.overviewUrl = strings.TrimRight(utils.Config.Frontend.BuildoorOverviewUrl, "/")

	if len(b.instanceHosts) == 0 && b.overviewUrl == "" {
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

	for _, instance := range instances {
		overview, err := b.fetchInstance(ctx, client, instance.URL)
		if err != nil {
			logger_buildoor.WithError(err).WithField("buildoor", instance.URL).Debug("skipping buildoor instance (overview fetch failed)")
			continue
		}

		if !overview.IsRegistered {
			unresolvedCount++
			continue
		}

		// prefer the service name (host label from the overview API); fall back to
		// deriving it from the URL when no label is available
		name := instance.Label
		if name == "" {
			name = deriveBuilderName(instance.URL)
		}

		newEntries[overview.BuilderIndex] = &buildoorEntry{
			name: name,
			url:  instance.URL,
		}
		resolvedCount++
	}

	b.entriesMutex.Lock()
	b.entries = newEntries
	b.entriesMutex.Unlock()

	logger_buildoor.Infof("buildoor inventory refreshed: %d resolved, %d unresolved", resolvedCount, unresolvedCount)
	return nil
}

func (b *BuildoorInventory) resolveInstances(ctx context.Context, client *http.Client) ([]buildoorHost, error) {
	if len(b.instanceHosts) > 0 {
		// directly configured URLs (optionally carrying a "label|url" service name)
		return b.instanceHosts, nil
	}
	if b.overviewUrl == "" {
		return nil, nil
	}

	hostsResp, err := b.fetchHosts(ctx, client)
	if err != nil {
		return nil, err
	}

	hosts := make([]buildoorHost, 0, len(hostsResp.Hosts))
	for _, h := range hostsResp.Hosts {
		if h.URL == "" {
			continue
		}
		h.URL = strings.TrimRight(h.URL, "/")
		hosts = append(hosts, h)
	}
	return hosts, nil
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
	rawIdx := builderIndex &^ BuilderIndexFlag
	b.entriesMutex.RLock()
	defer b.entriesMutex.RUnlock()
	if entry := b.entries[rawIdx]; entry != nil {
		return entry.name
	}
	return ""
}

func (b *BuildoorInventory) GetBuilderURL(builderIndex uint64) string {
	if b == nil {
		return ""
	}
	rawIdx := builderIndex &^ BuilderIndexFlag
	b.entriesMutex.RLock()
	defer b.entriesMutex.RUnlock()
	if entry := b.entries[rawIdx]; entry != nil {
		return entry.url
	}
	return ""
}

func deriveBuilderName(rawUrl string) string {
	host := rawUrl
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimRight(host, "/")

	// strip any path component
	if i := strings.IndexByte(host, '/'); i >= 0 {
		host = host[:i]
	}
	// strip the port (e.g. "host:8080" -> "host", "127.0.0.1:35239" -> "127.0.0.1")
	if i := strings.LastIndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}

	// bare IP addresses carry no meaningful service name; return empty so the
	// display falls back to the builder index instead of a misleading "127"
	if net.ParseIP(host) != nil {
		return ""
	}

	// use the first hostname label (e.g. "api-buildoor-foo.example.com" -> "api-buildoor-foo")
	if i := strings.Index(host, "."); i >= 0 {
		host = host[:i]
	}
	if stripped := strings.TrimPrefix(host, "api-buildoor-"); stripped != host {
		return stripped
	}
	return host
}
