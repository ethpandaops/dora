package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/blockdb"
	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/clients/sshtunnel"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/types"
	"github.com/ethpandaops/dora/utils"
	dynssz "github.com/pk910/dynamic-ssz"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
)

func blockdbSync() {
	flags := flag.NewFlagSet("blockdb-sync", flag.ExitOnError)
	configPath := flags.String("config", "", "Path to the config file")
	startEpoch := flags.Uint64("start", 0, "Start epoch")
	endEpoch := flags.Uint64("end", 0, "End epoch")
	clientName := flags.String("client", "", "Only use this specific client from config")
	concurrency := flags.Int("concurrency", 1, "Number of concurrent slot processors")
	verbose := flags.Bool("verbose", false, "Verbose output")
	flags.Parse(os.Args[1:])

	if *configPath == "" {
		fmt.Println("Error: config parameter is required")
		os.Exit(1)
	}

	if *endEpoch < *startEpoch {
		fmt.Println("Error: end epoch must be greater than start epoch")
		os.Exit(1)
	}

	if *concurrency < 1 {
		fmt.Println("Error: concurrency must be at least 1")
		os.Exit(1)
	}

	cfg := &types.Config{}
	err := utils.ReadConfig(cfg, *configPath)
	if err != nil {
		fmt.Printf("Error reading config file: %v\n", err)
		os.Exit(1)
	}
	utils.Config = cfg

	logger := logrus.New()
	if *verbose {
		logger.SetLevel(logrus.DebugLevel)
	} else {
		logger.SetLevel(logrus.InfoLevel)
	}

	// Initialize blockdb
	switch cfg.BlockDb.Engine {
	case "pebble":
		err := blockdb.InitWithPebble(cfg.BlockDb.Pebble)
		if err != nil {
			logger.Fatalf("Failed initializing pebble blockdb: %v", err)
		}
		logger.Infof("Pebble blockdb initialized at %v", cfg.BlockDb.Pebble.Path)
	default:
		logger.Fatal("No blockdb engine configured")
	}

	defer blockdb.GlobalBlockDb.Close()

	// Initialize client pool
	ctx := context.Background()
	pool := consensus.NewPool(ctx, logger.WithField("component", "cl-pool"))

	// Add consensus clients
	for _, endpoint := range cfg.BeaconApi.Endpoints {
		// Skip if client flag is set and doesn't match this endpoint
		if *clientName != "" && endpoint.Name != *clientName {
			continue
		}

		endpointConfig := &consensus.ClientConfig{
			URL:        endpoint.Url,
			Name:       endpoint.Name,
			Headers:    endpoint.Headers,
			DisableSSZ: cfg.KillSwitch.DisableSSZRequests,
		}

		if endpoint.Ssh != nil {
			endpointConfig.SshConfig = &sshtunnel.SshConfig{
				Host:     endpoint.Ssh.Host,
				Port:     endpoint.Ssh.Port,
				User:     endpoint.Ssh.User,
				Password: endpoint.Ssh.Password,
				Keyfile:  endpoint.Ssh.Keyfile,
			}
		}

		_, err := pool.AddEndpoint(endpointConfig)
		if err != nil {
			logger.Errorf("Could not add beacon client '%v' to pool: %v", endpoint.Name, err)
			continue
		}

		// If using specific client, we can break after adding it
		if *clientName != "" {
			break
		}
	}

	if len(pool.GetAllEndpoints()) == 0 {
		if *clientName != "" {
			logger.Fatalf("Client '%s' not found in config", *clientName)
		} else {
			logger.Fatal("No beacon clients configured")
		}
	}

	// Wait for chain specs
	chainState := pool.GetChainState()
	for chainState.GetSpecs() == nil {
		logger.Info("Waiting for chain specs...")
		time.Sleep(time.Second)
	}

	// initialize dynamic SSZ encoder
	staticSpec := map[string]any{}
	specYaml, err := yaml.Marshal(chainState.GetSpecs())
	if err == nil {
		yaml.Unmarshal(specYaml, &staticSpec)
	}
	dynSsz := dynssz.NewDynSsz(staticSpec)

	slotsPerEpoch := chainState.GetSpecs().SlotsPerEpoch
	startSlot := *startEpoch * slotsPerEpoch
	endSlot := (*endEpoch + 1) * slotsPerEpoch

	logger.Infof("Starting sync from epoch %d to %d (slots %d to %d)", *startEpoch, *endEpoch, startSlot, endSlot-1)

	// Create channels for work distribution and synchronization
	jobs := make(chan uint64, *concurrency)
	results := make(chan slotResult, *concurrency)
	done := make(chan bool)

	// Initialize epoch stats map
	epochStats := make(map[uint64]*epochSummary)
	for epoch := *startEpoch; epoch <= *endEpoch; epoch++ {
		epochStats[epoch] = &epochSummary{
			processed: make(map[string]int),
		}
	}

	// Start worker goroutines
	for i := 0; i < *concurrency; i++ {
		go func() {
			for slot := range jobs {
				result := processSlot(ctx, pool, dynSsz, slot, logger)
				results <- result
			}
		}()
	}

	// Start result collector
	go func() {
		for slot := startSlot; slot < endSlot; slot++ {
			result := <-results
			epoch := slot / slotsPerEpoch
			stats := epochStats[epoch]

			if result.err != nil {
				logger.Warn(result.err)
				stats.errors++
				continue
			}

			stats.processed[result.status]++
			stats.time += result.time
			// Print epoch summary when all slots in epoch are processed
			if (slot+1)%slotsPerEpoch == 0 || slot == endSlot-1 {
				stats.printSummary(logger, epoch)
				delete(epochStats, epoch) // Free memory
			}
		}
		done <- true
	}()

	// Send jobs
	for slot := startSlot; slot < endSlot; slot++ {
		jobs <- slot
	}
	close(jobs)

	// Wait for completion
	<-done

	logger.Info("Sync completed")
}

type slotResult struct {
	slot   uint64
	status string
	err    error
	time   time.Duration
}

type epochSummary struct {
	processed map[string]int
	errors    int
	time      time.Duration
}

func (e *epochSummary) printSummary(logger *logrus.Logger, epoch uint64) {
	logger.Infof("Epoch %d summary: added=%d, present=%d, missed=%d, errors=%d, time=%s", epoch, e.processed["added"], e.processed["present"], e.processed["missed"], e.errors, e.time)
}

func processSlot(ctx context.Context, pool *consensus.Pool, dynSsz *dynssz.DynSsz, slot uint64, logger *logrus.Logger) slotResult {
	t1 := time.Now()
	client := pool.GetReadyEndpoint(consensus.AnyClient)
	if client == nil {
		return slotResult{slot: slot, err: fmt.Errorf("no ready client found for slot %d", slot), time: time.Since(t1)}
	}

	log := logger.WithField("client", client.GetName())

	blockHeader, err := client.GetRPCClient().GetBlockHeaderBySlot(ctx, phase0.Slot(slot))
	if err != nil {
		return slotResult{slot: slot, err: fmt.Errorf("failed to get block for slot %d: %v", slot, err), time: time.Since(t1)}
	}

	if blockHeader == nil {
		log.Debugf("Slot %d: missed  (%.2f ms)", slot, time.Since(t1).Seconds()*1000)
		return slotResult{slot: slot, status: "missed", time: time.Since(t1)}
	}

	// Store block header
	headerBytes, err := blockHeader.Header.MarshalSSZ()
	if err != nil {
		return slotResult{slot: slot, err: fmt.Errorf("failed to marshal block header for slot %d: %v", slot, err), time: time.Since(t1)}
	}

	added, err := blockdb.GlobalBlockDb.AddBlockHeader(blockHeader.Root[:], 1, headerBytes)
	if err != nil {
		return slotResult{slot: slot, err: fmt.Errorf("failed to store block header for slot %d: %v", slot, err), time: time.Since(t1)}
	}

	if added {
		// Store block body only if header was newly added
		blockBody, err := client.GetRPCClient().GetBlockBodyByBlockroot(ctx, blockHeader.Root)
		if err != nil {
			return slotResult{slot: slot, err: fmt.Errorf("failed to get block body for slot %d: %v", slot, err), time: time.Since(t1)}
		}

		version, bodyBytes, err := beacon.MarshalVersionedSignedBeaconBlockSSZ(dynSsz, blockBody, true, false)
		if err != nil {
			return slotResult{slot: slot, err: fmt.Errorf("failed to marshal block body for slot %d: %v", slot, err), time: time.Since(t1)}
		}

		err = blockdb.GlobalBlockDb.AddBlockBody(blockHeader.Root[:], version, bodyBytes)
		if err != nil {
			return slotResult{slot: slot, err: fmt.Errorf("failed to store block body for slot %d: %v", slot, err), time: time.Since(t1)}
		}

		log.Debugf("Slot %d: added   (%.2f ms)", slot, time.Since(t1).Seconds()*1000)
		return slotResult{slot: slot, status: "added", time: time.Since(t1)}
	} else {
		log.Debugf("Slot %d: present (%.2f ms)", slot, time.Since(t1).Seconds()*1000)
		return slotResult{slot: slot, status: "present", time: time.Since(t1)}
	}
}
