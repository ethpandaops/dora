package services

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/clients/execution"
	exerpc "github.com/ethpandaops/dora/clients/execution/rpc"
	"github.com/ethpandaops/dora/clients/sshtunnel"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/types"
	"github.com/ethpandaops/dora/utils"
)

// Canonical mainnet deployments, used as defaults for registry/multicall addresses.
const (
	ensDefaultRegistry  = "0x00000000000C2E074eC69A0dFb2997BA6C7d2e1e"
	ensDefaultMulticall = "0xcA11bde05977b3631167028862bE2a173976CA11"
)

// ensProbeRetryInterval is how often incomplete probe results (missing registries or
// multicall) are re-checked, so contracts deployed after startup are picked up.
const ensProbeRetryInterval = 5 * time.Minute

// ensNetworkBackoff is how long a network is skipped by the resolve worker after a
// client/probe error, so one broken endpoint doesn't stall the whole batch pipeline.
const ensNetworkBackoff = 30 * time.Second

// EnsResolver resolves execution addresses to their primary ENS name on every
// configured network: the local network (the chain this explorer indexes, via the
// main execution pool) and any remote networks with dedicated RPC endpoints.
// Resolution is batched, asynchronous and persisted per (address, network) to the
// ens_names table. Handlers call ResolveNames once per page to warm the in-memory
// cache and feed the resolve queue.
type EnsResolver struct {
	ctx      context.Context
	logger   logrus.FieldLogger
	execPool *execution.Pool

	started  atomic.Bool
	networks []*ensNetwork

	cache *lru.Cache[common.Address, *ensCacheEntry]

	// forward resolution cache ("<network key>\x00<name>" -> address), used by search
	forwardCache *lru.Cache[string, *ensForwardCacheEntry]

	queue   chan common.Address
	pending sync.Map // common.Address -> struct{}
}

// ensNetwork is one ENS resolution source: the local network (the chain this explorer
// indexes, queried via the main execution pool) or a remote network with dedicated RPC
// endpoints (e.g. Ethereum mainnet on a devnet explorer).
type ensNetwork struct {
	key   string // DB/storage key ("" = local network)
	name  string // display name
	local bool

	registryAddrs []common.Address // configured (valid) registry addresses
	multicallAddr common.Address   // configured multicall address (zero = disabled)

	endpoints  []types.EndpointConfig // remote networks only
	clientInit sync.Once
	clients    []*ensEndpointClient

	failedUntil atomic.Int64 // resolve-worker backoff after client/probe errors (unix ts)

	// probe results: which contracts actually have bytecode on the network. Incomplete
	// results are re-probed every ensProbeRetryInterval so late deployments are found.
	probeMutex     sync.Mutex
	probed         bool
	probeTime      int64
	registries     []common.Address
	multicallReady bool
}

// ensProbeState is a consistent snapshot of a network's probe results for one resolve
// operation (the underlying state may change concurrently on re-probe).
type ensProbeState struct {
	registries     []common.Address
	multicallReady bool
	multicallAddr  common.Address
}

// getProbeState returns a snapshot of the network's probe results.
func (n *ensNetwork) getProbeState() ensProbeState {
	n.probeMutex.Lock()
	defer n.probeMutex.Unlock()
	return ensProbeState{
		registries:     n.registries,
		multicallReady: n.multicallReady,
		multicallAddr:  n.multicallAddr,
	}
}

// ResolvedEnsName is one resolved primary ENS name on a specific network.
type ResolvedEnsName struct {
	Name    string
	Network string // display name of the network
	Local   bool   // resolved on the chain this explorer indexes
}

// EnsForwardMatch is a forward-resolution result (name -> address) on one network.
type EnsForwardMatch struct {
	Address common.Address
	Network string
	Local   bool
}

// EnsPrefixMatch is a known (already reverse-resolved) name matching a search prefix.
type EnsPrefixMatch struct {
	Address common.Address
	Name    string
	Network string
	Local   bool
}

// ensCacheEntry is a cached lookup result for one address covering all configured
// networks. No names with full coverage is a negative result.
type ensCacheEntry struct {
	names        []*ResolvedEnsName // positive results in display order (local first)
	covered      int                // number of configured networks with a persisted result
	resolvedTime int64              // oldest resolve time across covered networks
}

// ensForwardCacheEntry is a cached forward-resolution result (name -> address).
// A zero address is a negative result.
type ensForwardCacheEntry struct {
	address      common.Address
	resolvedTime int64
}

// ensEndpointClient is a dedicated ENS RPC client with its configured name (for logs).
type ensEndpointClient struct {
	name   string
	client *exerpc.ExecutionClient
}

func NewEnsResolver(ctx context.Context, logger logrus.FieldLogger, execPool *execution.Pool) *EnsResolver {
	return &EnsResolver{
		ctx:      ctx,
		logger:   logger.WithField("service", "ens-resolver"),
		execPool: execPool,
	}
}

// EnsResolverNetworkStats is a per-network snapshot of probe/endpoint state for the
// debug page.
type EnsResolverNetworkStats struct {
	Name                 string
	Local                bool
	Endpoints            int // dedicated endpoints (0 for local = main execution pool)
	Probed               bool
	ConfiguredRegistries int
	Registries           []string // usable (bytecode-probed) registries, in priority order
	MulticallReady       bool
	MulticallAddress     string
}

// EnsResolverStats is a snapshot of the ENS resolver's runtime state for the debug page.
type EnsResolverStats struct {
	Enabled         bool
	QueueLen        int
	QueueCap        int
	CacheLen        int
	CacheCap        int
	ForwardCacheLen int
	RefreshPositive time.Duration
	RefreshNegative time.Duration
	Networks        []EnsResolverNetworkStats
}

// GetDebugStats returns a snapshot of the resolver's queue, cache and per-network
// probe state for the /debug/cache page. Safe to call when the resolver is disabled
// (nil-safe).
func (e *EnsResolver) GetDebugStats() *EnsResolverStats {
	stats := &EnsResolverStats{}
	if e == nil {
		return stats
	}

	cfg := &utils.Config.EnsResolver
	stats.Enabled = e.started.Load()
	stats.RefreshPositive = cfg.RefreshPositive
	stats.RefreshNegative = cfg.RefreshNegative

	if e.queue != nil {
		stats.QueueLen = len(e.queue)
		stats.QueueCap = cap(e.queue)
	}
	if e.cache != nil {
		stats.CacheLen = e.cache.Len()
		stats.CacheCap = cfg.CacheSize
	}
	if e.forwardCache != nil {
		stats.ForwardCacheLen = e.forwardCache.Len()
	}

	stats.Networks = make([]EnsResolverNetworkStats, 0, len(e.networks))
	for _, network := range e.networks {
		network.probeMutex.Lock()
		netStats := EnsResolverNetworkStats{
			Name:                 network.name,
			Local:                network.local,
			Endpoints:            len(network.endpoints),
			Probed:               network.probed,
			ConfiguredRegistries: len(network.registryAddrs),
			Registries:           make([]string, 0, len(network.registries)),
			MulticallReady:       network.multicallReady,
		}
		if network.multicallReady {
			netStats.MulticallAddress = network.multicallAddr.Hex()
		}
		for _, registry := range network.registries {
			netStats.Registries = append(netStats.Registries, registry.Hex())
		}
		network.probeMutex.Unlock()
		stats.Networks = append(stats.Networks, netStats)
	}

	return stats
}

// StartUpdater applies defaults, builds the network list, initializes the cache/queue
// and starts the worker.
func (e *EnsResolver) StartUpdater() {
	if e.started.Load() {
		return
	}

	cfg := &utils.Config.EnsResolver
	if cfg.RefreshPositive == 0 {
		cfg.RefreshPositive = 24 * time.Hour
	}
	if cfg.RefreshNegative == 0 {
		cfg.RefreshNegative = 6 * time.Hour
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 100
	}
	if cfg.QueueSize == 0 {
		cfg.QueueSize = 50000
	}
	if cfg.CacheSize == 0 {
		cfg.CacheSize = 50000
	}
	if len(cfg.RegistryAddresses) == 0 {
		cfg.RegistryAddresses = []string{ensDefaultRegistry}
	}
	if cfg.MulticallAddress == "" {
		cfg.MulticallAddress = ensDefaultMulticall
	}

	e.networks = e.buildNetworks()

	cache, err := lru.New[common.Address, *ensCacheEntry](cfg.CacheSize)
	if err != nil {
		e.logger.Errorf("failed to create ens name cache: %v", err)
		return
	}

	forwardCache, err := lru.New[string, *ensForwardCacheEntry](cfg.CacheSize)
	if err != nil {
		e.logger.Errorf("failed to create ens forward cache: %v", err)
		return
	}

	e.cache = cache
	e.forwardCache = forwardCache
	e.queue = make(chan common.Address, cfg.QueueSize)
	e.started.Store(true)

	go e.runUpdaterLoop()
}

// buildNetworks assembles the resolution sources from config: the local network first
// (resolved via the main execution pool), then the remote networks in config order.
func (e *EnsResolver) buildNetworks() []*ensNetwork {
	cfg := &utils.Config.EnsResolver

	localName := utils.Config.Chain.DisplayName
	if localName == "" {
		localName = "local"
	}

	networks := make([]*ensNetwork, 0, len(cfg.RemoteNetworks)+1)
	networks = append(networks, &ensNetwork{
		key:           "",
		name:          localName,
		local:         true,
		registryAddrs: e.parseEnsAddresses(cfg.RegistryAddresses, "local"),
		multicallAddr: e.parseEnsAddress(cfg.MulticallAddress, "local"),
	})

	seen := make(map[string]struct{}, len(cfg.RemoteNetworks))
	for i := range cfg.RemoteNetworks {
		remote := &cfg.RemoteNetworks[i]
		if remote.Name == "" {
			e.logger.Warnf("skipping ens remote network without a name")
			continue
		}
		if len(remote.Endpoints) == 0 {
			e.logger.Warnf("skipping ens remote network %q without endpoints", remote.Name)
			continue
		}
		if _, dup := seen[remote.Name]; dup {
			e.logger.Warnf("skipping duplicate ens remote network %q", remote.Name)
			continue
		}
		seen[remote.Name] = struct{}{}

		// unset contract addresses fall back to the top-level ensResolver config
		// (which itself defaults to the canonical deployments)
		registryAddrs := remote.RegistryAddresses
		if len(registryAddrs) == 0 {
			registryAddrs = cfg.RegistryAddresses
		}
		multicallAddr := remote.MulticallAddress
		if multicallAddr == "" {
			multicallAddr = cfg.MulticallAddress
		}

		networks = append(networks, &ensNetwork{
			key:           remote.Name,
			name:          remote.Name,
			registryAddrs: e.parseEnsAddresses(registryAddrs, remote.Name),
			multicallAddr: e.parseEnsAddress(multicallAddr, remote.Name),
			endpoints:     remote.Endpoints,
		})
	}

	return networks
}

// parseEnsAddresses parses configured hex addresses, dropping invalid entries with a
// warning.
func (e *EnsResolver) parseEnsAddresses(raw []string, networkName string) []common.Address {
	addrs := make([]common.Address, 0, len(raw))
	for _, entry := range raw {
		if !common.IsHexAddress(entry) {
			e.logger.Warnf("invalid ens registry address %q for network %q, skipping", entry, networkName)
			continue
		}
		addrs = append(addrs, common.HexToAddress(entry))
	}
	return addrs
}

// parseEnsAddress parses a configured hex address, returning the zero address (which
// disables the contract) for invalid entries.
func (e *EnsResolver) parseEnsAddress(raw, networkName string) common.Address {
	if !common.IsHexAddress(raw) {
		e.logger.Warnf("invalid ens multicall address %q for network %q, disabling multicall", raw, networkName)
		return common.Address{}
	}
	return common.HexToAddress(raw)
}

// networkByKey returns the configured network with the given storage key, or nil.
func (e *EnsResolver) networkByKey(key string) *ensNetwork {
	for _, network := range e.networks {
		if network.key == key {
			return network
		}
	}
	return nil
}

// ResolveNames returns the known names for the given addresses on all configured
// networks (keyed by lowercase 0x-hex, per-address list in display order: local network
// first). The cache is warmed with one batched DB query for cache misses; unresolved or
// stale addresses (including addresses missing results for a newly added network) are
// enqueued for asynchronous resolution.
//
// It is called by page handlers in the (uncached) build path — never from templates.
func (e *EnsResolver) ResolveNames(ctx context.Context, addrs [][]byte) map[string][]*ResolvedEnsName {
	result := make(map[string][]*ResolvedEnsName, len(addrs))
	if e == nil || !e.started.Load() || len(addrs) == 0 {
		return result
	}

	now := time.Now().Unix()
	seen := make(map[common.Address]struct{}, len(addrs))
	misses := make([][]byte, 0, len(addrs))

	for _, raw := range addrs {
		if len(raw) != 20 {
			continue
		}
		addr := common.BytesToAddress(raw)
		if _, ok := seen[addr]; ok {
			continue
		}
		seen[addr] = struct{}{}

		if entry, ok := e.cache.Get(addr); ok {
			if len(entry.names) > 0 {
				result[strings.ToLower(addr.Hex())] = entry.names
			}
			if e.isStale(entry, now) {
				e.enqueue(addr)
			}
			continue
		}

		misses = append(misses, addr.Bytes())
	}

	if len(misses) == 0 {
		return result
	}

	dbEntries, err := db.GetEnsNamesByAddresses(ctx, misses)
	if err != nil {
		e.logger.Warnf("failed to load ens names from db: %v", err)
		dbEntries = nil
	}

	for _, raw := range misses {
		addr := common.BytesToAddress(raw)
		rows, ok := dbEntries[hex.EncodeToString(raw)]
		if !ok {
			// never resolved yet
			e.enqueue(addr)
			continue
		}

		entry := e.cacheEntryFromRows(rows)
		e.cache.Add(addr, entry)
		if len(entry.names) > 0 {
			result[strings.ToLower(addr.Hex())] = entry.names
		}
		if e.isStale(entry, now) {
			e.enqueue(addr)
		}
	}

	return result
}

// cacheEntryFromRows builds a cache entry from the persisted per-network rows of one
// address, ordering positive names by network priority (local first). Rows of networks
// that are no longer configured are ignored.
func (e *EnsResolver) cacheEntryFromRows(rows []*dbtypes.EnsName) *ensCacheEntry {
	byKey := make(map[string]*dbtypes.EnsName, len(rows))
	for _, row := range rows {
		byKey[row.Network] = row
	}

	entry := &ensCacheEntry{}
	for _, network := range e.networks {
		row, ok := byKey[network.key]
		if !ok {
			continue
		}
		entry.covered++
		if entry.resolvedTime == 0 || row.ResolvedTime < entry.resolvedTime {
			entry.resolvedTime = row.ResolvedTime
		}
		if row.Name != "" {
			entry.names = append(entry.names, &ResolvedEnsName{
				Name:    row.Name,
				Network: network.name,
				Local:   network.local,
			})
		}
	}
	return entry
}

// isStale reports whether an entry should be re-resolved: it lacks a result for a
// configured network, or it is older than its refresh interval (positive and negative
// results use separate intervals).
func (e *EnsResolver) isStale(entry *ensCacheEntry, now int64) bool {
	if entry.covered < len(e.networks) {
		return true
	}
	refresh := utils.Config.EnsResolver.RefreshPositive
	if len(entry.names) == 0 {
		refresh = utils.Config.EnsResolver.RefreshNegative
	}
	if refresh <= 0 {
		return false
	}
	return now-entry.resolvedTime > int64(refresh/time.Second)
}

// ResolveEnsName forward-resolves an ENS name (EIP-137) on every configured network,
// using synchronous eth_calls on cache miss (uncached networks are queried
// concurrently). Results keep network display order (local first) and only contain
// networks where the name resolved to a non-zero address.
//
// It is called from the search handlers — results are cached here (LRU) and again at
// the page-cache layer, so on-chain lookups stay bounded.
func (e *EnsResolver) ResolveEnsName(ctx context.Context, name string) []*EnsForwardMatch {
	if e == nil || !e.started.Load() {
		return nil
	}

	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || !strings.Contains(name, ".") {
		return nil
	}

	now := time.Now().Unix()
	matches := make([]*EnsForwardMatch, len(e.networks))
	pending := make([]*ensNetwork, 0, len(e.networks))
	pendingIdx := make([]int, 0, len(e.networks))

	for i, network := range e.networks {
		if entry, ok := e.forwardCache.Get(network.key + "\x00" + name); ok && !e.isForwardStale(entry, now) {
			if entry.address != (common.Address{}) {
				matches[i] = &EnsForwardMatch{Address: entry.address, Network: network.name, Local: network.local}
			}
			continue
		}
		pending = append(pending, network)
		pendingIdx = append(pendingIdx, i)
	}

	if len(pending) > 0 {
		resolveCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		var wg sync.WaitGroup
		for j := range pending {
			wg.Add(1)
			go func(idx int, network *ensNetwork) {
				defer wg.Done()
				addr, err := e.resolveForwardOnNetwork(resolveCtx, network, name)
				if err != nil {
					e.logger.Warnf("ens forward resolve %q on network %q: %v", name, network.name, err)
					return
				}
				e.forwardCache.Add(network.key+"\x00"+name, &ensForwardCacheEntry{address: addr, resolvedTime: time.Now().Unix()})
				if addr != (common.Address{}) {
					// each goroutine writes a distinct slice index, so no lock is needed
					matches[idx] = &EnsForwardMatch{Address: addr, Network: network.name, Local: network.local}
				}
			}(pendingIdx[j], pending[j])
		}
		wg.Wait()
	}

	out := make([]*EnsForwardMatch, 0, len(matches))
	for _, match := range matches {
		if match != nil {
			out = append(out, match)
		}
	}
	return out
}

// resolveForwardOnNetwork forward-resolves a name on one network (client + probe +
// registry lookups).
func (e *EnsResolver) resolveForwardOnNetwork(ctx context.Context, network *ensNetwork, name string) (common.Address, error) {
	ethClient, err := e.getEthClient(ctx, network)
	if err != nil {
		return common.Address{}, err
	}
	if err := e.ensureProbed(ctx, network, ethClient); err != nil {
		return common.Address{}, err
	}
	return e.resolveForward(ctx, ethClient, network.getProbeState(), name), nil
}

// isForwardStale reports whether a forward cache entry is past its refresh interval
// (positive and negative results use separate intervals).
func (e *EnsResolver) isForwardStale(entry *ensForwardCacheEntry, now int64) bool {
	refresh := utils.Config.EnsResolver.RefreshPositive
	if entry.address == (common.Address{}) {
		refresh = utils.Config.EnsResolver.RefreshNegative
	}
	if refresh <= 0 {
		return false
	}
	return now-entry.resolvedTime > int64(refresh/time.Second)
}

// GetCachedNamesByPrefix returns already-resolved ENS names starting with the given
// prefix from the DB, translating stored network keys to display names. Rows of
// networks that are no longer configured are dropped.
func (e *EnsResolver) GetCachedNamesByPrefix(ctx context.Context, prefix string, limit int) []*EnsPrefixMatch {
	if e == nil || !e.started.Load() {
		return nil
	}
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if prefix == "" {
		return nil
	}

	rows, err := db.GetEnsNamesByPrefix(ctx, prefix, limit)
	if err != nil {
		return nil
	}

	matches := make([]*EnsPrefixMatch, 0, len(rows))
	for _, row := range rows {
		network := e.networkByKey(row.Network)
		if network == nil {
			continue
		}
		matches = append(matches, &EnsPrefixMatch{
			Address: common.BytesToAddress(row.Address),
			Name:    row.Name,
			Network: network.name,
			Local:   network.local,
		})
	}
	return matches
}

// enqueue adds an address to the capped resolve queue, de-duplicating pending entries
// and dropping (best-effort) when the queue is full.
func (e *EnsResolver) enqueue(addr common.Address) {
	if _, loaded := e.pending.LoadOrStore(addr, struct{}{}); loaded {
		return
	}
	select {
	case e.queue <- addr:
	default:
		e.pending.Delete(addr)
	}
}

func (e *EnsResolver) runUpdaterLoop() {
	defer utils.HandleSubroutinePanic("EnsResolver.runUpdaterLoop", e.runUpdaterLoop)

	for {
		select {
		case <-e.ctx.Done():
			return
		case first := <-e.queue:
			e.pending.Delete(first)
			batch := e.gatherBatch(first)
			if err := e.processBatch(batch); err != nil {
				e.logger.Errorf("ens resolve batch error: %v, retrying in 30 sec...", err)
				// re-queue the batch and back off before trying again
				for _, addr := range batch {
					e.enqueue(addr)
				}
				time.Sleep(30 * time.Second)
			}
		}
	}
}

// gatherBatch collects up to BatchSize queued addresses, starting with first.
func (e *EnsResolver) gatherBatch(first common.Address) []common.Address {
	batchSize := utils.Config.EnsResolver.BatchSize
	batch := make([]common.Address, 0, batchSize)
	batch = append(batch, first)

	for len(batch) < batchSize {
		select {
		case addr := <-e.queue:
			e.pending.Delete(addr)
			batch = append(batch, addr)
		default:
			return batch
		}
	}
	return batch
}

// processBatch resolves a batch of addresses on every configured network and persists
// the per-network results (positive and negative) to the cache and DB. Networks that
// fail with a client error are skipped for ensNetworkBackoff and get no persisted
// result, so their coverage stays missing and affected addresses are re-enqueued on
// the next page view. An error is returned only when every network failed.
func (e *EnsResolver) processBatch(batch []common.Address) error {
	if len(batch) == 0 {
		return nil
	}

	now := time.Now().Unix()
	succeeded := make([]*ensNetwork, 0, len(e.networks))
	names := make(map[string]map[common.Address]string, len(e.networks))

	for _, network := range e.networks {
		if now < network.failedUntil.Load() {
			continue
		}
		resolved, err := e.resolveBatchOnNetwork(network, batch)
		if err != nil {
			network.failedUntil.Store(time.Now().Unix() + int64(ensNetworkBackoff/time.Second))
			e.logger.Warnf("ens resolve batch on network %q failed: %v", network.name, err)
			continue
		}
		succeeded = append(succeeded, network)
		names[network.key] = resolved
	}
	if len(succeeded) == 0 {
		return fmt.Errorf("ens resolution failed on all %d networks", len(e.networks))
	}

	now = time.Now().Unix()
	dbNames := make([]*dbtypes.EnsName, 0, len(batch)*len(succeeded))
	for _, addr := range batch {
		rows := make([]*dbtypes.EnsName, 0, len(succeeded))
		for _, network := range succeeded {
			rows = append(rows, &dbtypes.EnsName{
				Address:      addr.Bytes(),
				Network:      network.key,
				Name:         names[network.key][addr],
				ResolvedTime: now,
			})
		}
		dbNames = append(dbNames, rows...)

		if len(succeeded) == len(e.networks) {
			e.cache.Add(addr, e.cacheEntryFromRows(rows))
		} else {
			// partial result: drop the cache entry so the next lookup rebuilds it from
			// the DB (merging older rows of the failed networks) and re-enqueues.
			e.cache.Remove(addr)
		}
	}

	err := db.RunDBTransaction(func(tx *sqlx.Tx) error {
		return db.InsertEnsNames(e.ctx, tx, dbNames)
	})
	if err != nil {
		e.logger.Warnf("failed to persist ens names: %v", err)
	}

	return nil
}

// resolveBatchOnNetwork resolves the batch on a single network (client + probe +
// registry lookups). With no usable registry all addresses resolve to no name, which
// gets persisted as a negative result so they aren't re-queued until RefreshNegative
// elapses (avoids spinning on chains without ENS; the periodic re-probe picks up
// registries deployed later).
func (e *EnsResolver) resolveBatchOnNetwork(network *ensNetwork, batch []common.Address) (map[common.Address]string, error) {
	ctx, cancel := context.WithTimeout(e.ctx, 60*time.Second)
	defer cancel()

	ethClient, err := e.getEthClient(ctx, network)
	if err != nil {
		return nil, err
	}
	if err := e.ensureProbed(ctx, network, ethClient); err != nil {
		return nil, err
	}

	probeState := network.getProbeState()
	if len(probeState.registries) == 0 {
		return map[common.Address]string{}, nil
	}
	return e.resolveBatch(ctx, ethClient, probeState, batch), nil
}

// ensureProbed checks which configured registries and the multicall contract are
// actually deployed on the network. Complete results are final; incomplete results
// (missing registries or multicall) are re-probed every ensProbeRetryInterval, so
// contracts deployed after startup are picked up. A client error is treated as
// transient (returns error, keeps the previous state); missing bytecode marks the
// contract unusable until the next probe.
func (e *EnsResolver) ensureProbed(ctx context.Context, network *ensNetwork, ethClient *ethclient.Client) error {
	network.probeMutex.Lock()
	defer network.probeMutex.Unlock()

	now := time.Now().Unix()
	if network.probed {
		complete := len(network.registries) == len(network.registryAddrs) &&
			(network.multicallReady || network.multicallAddr == (common.Address{}))
		if complete || now-network.probeTime < int64(ensProbeRetryInterval/time.Second) {
			return nil
		}
	}
	firstProbe := !network.probed

	registries := make([]common.Address, 0, len(network.registryAddrs))
	for _, addr := range network.registryAddrs {
		code, err := ethClient.CodeAt(ctx, addr, nil)
		if err != nil {
			return fmt.Errorf("probing ens registry %s on network %q: %w", addr.Hex(), network.name, err)
		}
		if len(code) == 0 {
			if firstProbe {
				e.logger.Warnf("ens registry %s has no bytecode on network %q, skipping until deployed", addr.Hex(), network.name)
			}
			continue
		}
		registries = append(registries, addr)
	}

	multicallReady := false
	if network.multicallAddr != (common.Address{}) {
		code, err := ethClient.CodeAt(ctx, network.multicallAddr, nil)
		if err != nil {
			return fmt.Errorf("probing multicall %s on network %q: %w", network.multicallAddr.Hex(), network.name, err)
		}
		multicallReady = len(code) > 0
		if !multicallReady && firstProbe {
			e.logger.Warnf("multicall %s not deployed on network %q, falling back to individual calls", network.multicallAddr.Hex(), network.name)
		}
	}

	if firstProbe || len(registries) != len(network.registries) || multicallReady != network.multicallReady {
		e.logger.Infof("ens network %q probed: %d/%d usable registries, multicall=%v",
			network.name, len(registries), len(network.registryAddrs), multicallReady)
	}

	network.registries = registries
	network.multicallReady = multicallReady
	network.probed = true
	network.probeTime = now
	return nil
}

// getEthClient returns an eth client for ENS lookups on the given network: a ready
// client from the main execution pool for the local network, or one of the network's
// dedicated endpoints otherwise.
func (e *EnsResolver) getEthClient(ctx context.Context, network *ensNetwork) (*ethclient.Client, error) {
	if network.local {
		client := e.execPool.GetReadyEndpoint(execution.AnyClient)
		if client == nil {
			return nil, fmt.Errorf("no ready execution client available for ens resolution")
		}
		ethClient := client.GetRPCClient().GetEthClient()
		if ethClient == nil {
			return nil, fmt.Errorf("execution client has no eth client")
		}
		return ethClient, nil
	}

	network.clientInit.Do(func() { e.initNetworkClients(network) })
	for _, ec := range network.clients {
		if err := ec.client.Initialize(ctx); err != nil {
			e.logger.Warnf("ens endpoint %s (network %q) init failed: %v", ec.name, network.name, err)
			continue
		}
		if ethClient := ec.client.GetEthClient(); ethClient != nil {
			return ethClient, nil
		}
	}
	return nil, fmt.Errorf("no usable ens endpoint for network %q", network.name)
}

// initNetworkClients builds RPC clients from a remote network's configured endpoints.
func (e *EnsResolver) initNetworkClients(network *ensNetwork) {
	clients := make([]*ensEndpointClient, 0, len(network.endpoints))

	for i := range network.endpoints {
		endpoint := network.endpoints[i]
		processed, err := applyAuthGroupToEndpoint(&endpoint)
		if err != nil {
			e.logger.Warnf("could not apply authGroup to ens endpoint %q: %v", endpoint.Name, err)
			processed = &endpoint
		}

		var sshConfig *sshtunnel.SshConfig
		if processed.Ssh != nil {
			sshConfig = &sshtunnel.SshConfig{
				Host:     processed.Ssh.Host,
				Port:     processed.Ssh.Port,
				User:     processed.Ssh.User,
				Password: processed.Ssh.Password,
				Keyfile:  processed.Ssh.Keyfile,
			}
		}

		clientName := processed.Name
		if clientName == "" {
			clientName = fmt.Sprintf("%s-%d", network.name, i)
		}

		client, err := exerpc.NewExecutionClient(clientName, processed.Url, processed.Headers, sshConfig, e.logger.WithField("ens-endpoint", clientName))
		if err != nil {
			e.logger.Warnf("could not create ens endpoint %q: %v", clientName, err)
			continue
		}
		clients = append(clients, &ensEndpointClient{name: clientName, client: client})
	}

	network.clients = clients
}
