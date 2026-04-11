// Package statecache provides an optional file-system-backed cache for beacon states.
// States are stored as compressed SSZ files keyed by (dependentRoot, targetEpoch).
// The cache limits the number of stored states and re-initializes from the
// filesystem on restart (no in-memory index — just scans the directory).
package statecache

import (
	"compress/gzip"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/fulu"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/types"

	dynssz "github.com/pk910/dynamic-ssz"
)

// StateCache manages cached beacon states on the local filesystem.
// It is safe for concurrent use.
type StateCache struct {
	mu        sync.Mutex
	path      string
	maxStates uint
	dynSsz    *dynssz.DynSsz
}

// New creates a new StateCache. Returns nil if the config disables caching.
// The directory is created if it doesn't exist.
func New(cfg *types.Config, dynSsz *dynssz.DynSsz) *StateCache {
	scCfg := cfg.Indexer.StateCache
	if !scCfg.Enabled || scCfg.Path == "" {
		return nil
	}

	maxStates := scCfg.MaxStates
	if maxStates == 0 {
		maxStates = 5
	}

	if err := os.MkdirAll(scCfg.Path, 0o750); err != nil {
		return nil
	}

	return &StateCache{
		path:      scCfg.Path,
		maxStates: maxStates,
		dynSsz:    dynSsz,
	}
}

// stateKey identifies a cached state by dependent root and target epoch.
type stateKey struct {
	DependentRoot phase0.Root
	TargetEpoch   phase0.Epoch
}

// filename returns the cache filename for a state key.
// Format: <epoch>_<rootHex>.ssz.gz
func (k stateKey) filename() string {
	return fmt.Sprintf("%d_%s.ssz.gz", k.TargetEpoch, hex.EncodeToString(k.DependentRoot[:]))
}

// parseFilename parses a cache filename back into a stateKey.
// Returns false if the filename doesn't match the expected format.
func parseFilename(name string) (stateKey, bool) {
	name = strings.TrimSuffix(name, ".ssz.gz")
	parts := strings.SplitN(name, "_", 2)
	if len(parts) != 2 {
		return stateKey{}, false
	}

	epoch, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return stateKey{}, false
	}

	rootBytes, err := hex.DecodeString(parts[1])
	if err != nil || len(rootBytes) != 32 {
		return stateKey{}, false
	}

	var key stateKey
	key.TargetEpoch = phase0.Epoch(epoch)
	copy(key.DependentRoot[:], rootBytes)
	return key, true
}

// Check returns true if a cached state exists for the given key.
func (sc *StateCache) Check(dependentRoot phase0.Root, targetEpoch phase0.Epoch) bool {
	if sc == nil {
		return false
	}

	key := stateKey{DependentRoot: dependentRoot, TargetEpoch: targetEpoch}
	path := filepath.Join(sc.path, key.filename())
	_, err := os.Stat(path)
	return err == nil
}

// Load reads a cached state from disk. Returns nil if not found.
func (sc *StateCache) Load(dependentRoot phase0.Root, targetEpoch phase0.Epoch) *spec.VersionedBeaconState {
	if sc == nil {
		return nil
	}

	key := stateKey{DependentRoot: dependentRoot, TargetEpoch: targetEpoch}
	path := filepath.Join(sc.path, key.filename())

	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil
	}
	defer gz.Close()

	sszData, err := io.ReadAll(gz)
	if err != nil {
		return nil
	}

	// Read version marker (first byte)
	if len(sszData) < 1 {
		return nil
	}
	version := spec.DataVersion(sszData[0])
	sszData = sszData[1:]

	state, err := unmarshalState(sc.dynSsz, version, sszData)
	if err != nil {
		return nil
	}

	return state
}

// Store writes a state to disk and enforces the max states limit.
func (sc *StateCache) Store(dependentRoot phase0.Root, targetEpoch phase0.Epoch, state *spec.VersionedBeaconState) error {
	if sc == nil {
		return nil
	}

	sc.mu.Lock()
	defer sc.mu.Unlock()

	sszData, err := marshalState(sc.dynSsz, state)
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	// Prepend version marker
	versioned := make([]byte, 1+len(sszData))
	versioned[0] = byte(state.Version)
	copy(versioned[1:], sszData)

	key := stateKey{DependentRoot: dependentRoot, TargetEpoch: targetEpoch}
	path := filepath.Join(sc.path, key.filename())

	if err := os.MkdirAll(sc.path, 0o750); err != nil {
		return fmt.Errorf("failed to ensure cache directory: %w", err)
	}

	f, err := os.CreateTemp(sc.path, "state-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := f.Name()

	gz := gzip.NewWriter(f)
	if _, err := gz.Write(versioned); err != nil {
		gz.Close()
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("failed to write compressed data: %w", err)
	}
	gz.Close()
	f.Close()

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	// Enforce max states limit
	sc.evict()

	return nil
}

// evict removes the oldest cached states to stay within the max limit.
// Must be called with sc.mu held.
func (sc *StateCache) evict() {
	entries, err := os.ReadDir(sc.path)
	if err != nil {
		return
	}

	type cachedEntry struct {
		name    string
		modTime int64
	}

	var cached []cachedEntry
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".ssz.gz") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		cached = append(cached, cachedEntry{name: entry.Name(), modTime: info.ModTime().UnixNano()})
	}

	if uint(len(cached)) <= sc.maxStates {
		return
	}

	// Sort by modification time ascending (oldest first)
	sort.Slice(cached, func(i, j int) bool {
		return cached[i].modTime < cached[j].modTime
	})

	// Remove oldest until within limit
	toRemove := uint(len(cached)) - sc.maxStates
	for i := uint(0); i < toRemove; i++ {
		os.Remove(filepath.Join(sc.path, cached[i].name))
	}
}

// marshalState serializes a VersionedBeaconState to SSZ bytes.
func marshalState(dynSsz *dynssz.DynSsz, state *spec.VersionedBeaconState) ([]byte, error) {
	switch state.Version {
	case spec.DataVersionFulu:
		if state.Fulu == nil {
			return nil, fmt.Errorf("nil fulu state")
		}
		if dynSsz != nil {
			return dynSsz.MarshalSSZ(state.Fulu)
		}
		return state.Fulu.MarshalSSZ()
	case spec.DataVersionGloas:
		if state.Gloas == nil {
			return nil, fmt.Errorf("nil gloas state")
		}
		if dynSsz != nil {
			return dynSsz.MarshalSSZ(state.Gloas)
		}
		return state.Gloas.MarshalSSZ()
	default:
		return nil, fmt.Errorf("unsupported state version: %v", state.Version)
	}
}

// unmarshalState deserializes SSZ bytes into a VersionedBeaconState.
func unmarshalState(dynSsz *dynssz.DynSsz, version spec.DataVersion, data []byte) (*spec.VersionedBeaconState, error) {
	state := &spec.VersionedBeaconState{Version: version}

	switch version {
	case spec.DataVersionFulu:
		state.Fulu = new(fulu.BeaconState)
		var err error
		if dynSsz != nil {
			err = dynSsz.UnmarshalSSZ(state.Fulu, data)
		} else {
			err = state.Fulu.UnmarshalSSZ(data)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal fulu state: %w", err)
		}
	case spec.DataVersionGloas:
		state.Gloas = new(gloas.BeaconState)
		var err error
		if dynSsz != nil {
			err = dynSsz.UnmarshalSSZ(state.Gloas, data)
		} else {
			err = state.Gloas.UnmarshalSSZ(data)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal gloas state: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported state version: %v", version)
	}

	return state, nil
}
