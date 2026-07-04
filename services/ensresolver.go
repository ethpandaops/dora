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
	"github.com/ethpandaops/dora/utils"
)

var logger_ens = logrus.StandardLogger().WithField("module", "ens_resolver")

// EnsResolver resolves execution addresses to their primary ENS name. Resolution is
// batched, asynchronous and persisted to the ens_names table. Handlers call
// ResolveNames once per page to warm the in-memory cache and feed the resolve queue.
type EnsResolver struct {
	ctx      context.Context
	logger   logrus.FieldLogger
	execPool *execution.Pool

	started atomic.Bool
	cache   *lru.Cache[common.Address, *ensCacheEntry]

	queue   chan common.Address
	pending sync.Map // common.Address -> struct{}

	// dedicated RPC clients built from EnsResolver.Endpoints (lazy init)
	dedicatedInit    sync.Once
	dedicatedClients []*ensEndpointClient

	// probe results (worker-goroutine only, guarded by probeMutex until probed)
	probeMutex       sync.Mutex
	probed           bool
	registries       []common.Address
	multicallAddress common.Address
	multicallReady   bool
}

// ensCacheEntry is a cached lookup result. An empty name is a negative result.
type ensCacheEntry struct {
	name         string
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

// EnsResolverStats is a snapshot of the ENS resolver's runtime state for the debug page.
type EnsResolverStats struct {
	Enabled              bool
	Probed               bool
	QueueLen             int
	QueueCap             int
	CacheLen             int
	CacheCap             int
	ConfiguredRegistries int
	Registries           []string // usable (bytecode-probed) registries, in priority order
	MulticallReady       bool
	MulticallAddress     string
	RefreshPositive      time.Duration
	RefreshNegative      time.Duration
}

// GetDebugStats returns a snapshot of the resolver's queue, cache and probed registries
// for the /debug/cache page. Safe to call when the resolver is disabled (nil-safe).
func (e *EnsResolver) GetDebugStats() *EnsResolverStats {
	stats := &EnsResolverStats{}
	if e == nil {
		return stats
	}

	cfg := &utils.Config.EnsResolver
	stats.Enabled = e.started.Load()
	stats.ConfiguredRegistries = len(cfg.RegistryAddresses)
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

	e.probeMutex.Lock()
	stats.Probed = e.probed
	stats.MulticallReady = e.multicallReady
	if e.multicallReady {
		stats.MulticallAddress = e.multicallAddress.Hex()
	}
	for _, registry := range e.registries {
		stats.Registries = append(stats.Registries, registry.Hex())
	}
	e.probeMutex.Unlock()

	return stats
}

// StartUpdater applies defaults, initializes the cache/queue and starts the worker.
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
		cfg.RegistryAddresses = []string{"0x00000000000C2E074eC69A0dFb2997BA6C7d2e1e"}
	}
	if cfg.MulticallAddress == "" {
		cfg.MulticallAddress = "0xcA11bde05977b3631167028862bE2a173976CA11"
	}

	cache, err := lru.New[common.Address, *ensCacheEntry](cfg.CacheSize)
	if err != nil {
		e.logger.Errorf("failed to create ens name cache: %v", err)
		return
	}

	e.cache = cache
	e.queue = make(chan common.Address, cfg.QueueSize)
	e.started.Store(true)

	go e.runUpdaterLoop()
}

// ResolveNames returns the known primary names for the given addresses (keyed by
// lowercase 0x-hex), warming the cache with one batched DB query for cache misses and
// enqueuing unresolved or stale addresses for asynchronous resolution.
//
// It is called by page handlers in the (uncached) build path — never from templates.
func (e *EnsResolver) ResolveNames(ctx context.Context, addrs [][]byte) map[string]string {
	result := make(map[string]string)
	if e == nil || !e.started.Load() || len(addrs) == 0 {
		return result
	}

	now := time.Now().Unix()
	seen := make(map[common.Address]struct{}, len(addrs))
	misses := make([][]byte, 0)

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
			if entry.name != "" {
				result[strings.ToLower(addr.Hex())] = entry.name
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
		dbEntry, ok := dbEntries[hex.EncodeToString(raw)]
		if !ok {
			// never resolved yet
			e.enqueue(addr)
			continue
		}

		entry := &ensCacheEntry{name: dbEntry.Name, resolvedTime: dbEntry.ResolvedTime}
		e.cache.Add(addr, entry)
		if entry.name != "" {
			result[strings.ToLower(addr.Hex())] = entry.name
		}
		if e.isStale(entry, now) {
			e.enqueue(addr)
		}
	}

	return result
}

// isStale reports whether an entry is older than its refresh interval and should be
// re-resolved (positive and negative results use separate intervals).
func (e *EnsResolver) isStale(entry *ensCacheEntry, now int64) bool {
	refresh := utils.Config.EnsResolver.RefreshPositive
	if entry.name == "" {
		refresh = utils.Config.EnsResolver.RefreshNegative
	}
	if refresh <= 0 {
		return false
	}
	return now-entry.resolvedTime > int64(refresh/time.Second)
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

// processBatch resolves a batch of addresses and persists the results (positive and
// negative) to the cache and DB.
func (e *EnsResolver) processBatch(batch []common.Address) error {
	if len(batch) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(e.ctx, 60*time.Second)
	defer cancel()

	ethClient, err := e.getEthClient(ctx)
	if err != nil {
		return err
	}

	if err := e.ensureProbed(ctx, ethClient); err != nil {
		return err
	}

	names := map[common.Address]string{}
	if len(e.registries) > 0 {
		names = e.resolveBatch(ctx, ethClient, batch)
	}
	// with no usable registry, all addresses are persisted as negative so they aren't
	// re-queued until RefreshNegative elapses (avoids spinning on chains without ENS).

	now := time.Now().Unix()
	dbNames := make([]*dbtypes.EnsName, 0, len(batch))
	for _, addr := range batch {
		name := names[addr]
		e.cache.Add(addr, &ensCacheEntry{name: name, resolvedTime: now})
		dbNames = append(dbNames, &dbtypes.EnsName{Address: addr.Bytes(), Name: name, ResolvedTime: now})
	}

	err = db.RunDBTransaction(func(tx *sqlx.Tx) error {
		return db.InsertEnsNames(e.ctx, tx, dbNames)
	})
	if err != nil {
		e.logger.Warnf("failed to persist ens names: %v", err)
	}

	return nil
}

// ensureProbed checks (once) which configured registries and the Multicall3 contract
// are actually deployed on the target chain. A client error is treated as transient
// (returns error, leaves unprobed); missing bytecode marks the contract unusable.
func (e *EnsResolver) ensureProbed(ctx context.Context, ethClient *ethclient.Client) error {
	e.probeMutex.Lock()
	defer e.probeMutex.Unlock()

	if e.probed {
		return nil
	}

	cfg := &utils.Config.EnsResolver
	registries := make([]common.Address, 0, len(cfg.RegistryAddresses))
	for _, raw := range cfg.RegistryAddresses {
		if !common.IsHexAddress(raw) {
			e.logger.Warnf("invalid ens registry address %q, skipping", raw)
			continue
		}
		addr := common.HexToAddress(raw)
		code, err := ethClient.CodeAt(ctx, addr, nil)
		if err != nil {
			return fmt.Errorf("probing ens registry %s: %w", raw, err)
		}
		if len(code) == 0 {
			e.logger.Warnf("ens registry %s has no bytecode on this chain, skipping", raw)
			continue
		}
		registries = append(registries, addr)
	}

	multicallReady := false
	if common.IsHexAddress(cfg.MulticallAddress) {
		mc := common.HexToAddress(cfg.MulticallAddress)
		code, err := ethClient.CodeAt(ctx, mc, nil)
		if err != nil {
			return fmt.Errorf("probing multicall %s: %w", cfg.MulticallAddress, err)
		}
		if len(code) > 0 {
			e.multicallAddress = mc
			multicallReady = true
		} else {
			e.logger.Warnf("multicall %s not deployed, falling back to individual calls", cfg.MulticallAddress)
		}
	}

	e.registries = registries
	e.multicallReady = multicallReady
	e.probed = true
	e.logger.Infof("ens resolver ready: %d usable registries, multicall=%v", len(registries), multicallReady)
	return nil
}

// getEthClient returns an eth client for ENS lookups, preferring dedicated endpoints
// and falling back to a ready client from the main execution pool.
func (e *EnsResolver) getEthClient(ctx context.Context) (*ethclient.Client, error) {
	if len(utils.Config.EnsResolver.Endpoints) > 0 {
		e.dedicatedInit.Do(e.initDedicatedClients)
		for _, ec := range e.dedicatedClients {
			if err := ec.client.Initialize(ctx); err != nil {
				e.logger.Warnf("ens endpoint %s init failed: %v", ec.name, err)
				continue
			}
			if ethClient := ec.client.GetEthClient(); ethClient != nil {
				return ethClient, nil
			}
		}
		return nil, fmt.Errorf("no usable dedicated ens endpoint")
	}

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

// initDedicatedClients builds RPC clients from the configured ENS endpoints.
func (e *EnsResolver) initDedicatedClients() {
	endpoints := utils.Config.EnsResolver.Endpoints
	clients := make([]*ensEndpointClient, 0, len(endpoints))

	for i := range endpoints {
		endpoint := endpoints[i]
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

		client, err := exerpc.NewExecutionClient(processed.Name, processed.Url, processed.Headers, sshConfig, e.logger.WithField("ens-endpoint", processed.Name))
		if err != nil {
			e.logger.Warnf("could not create ens endpoint %q: %v", processed.Name, err)
			continue
		}
		clients = append(clients, &ensEndpointClient{name: processed.Name, client: client})
	}

	e.dedicatedClients = clients
}
