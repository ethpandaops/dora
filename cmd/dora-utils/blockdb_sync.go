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
	startSlot := flags.Uint64("start", 0, "Start slot")
	endSlot := flags.Uint64("end", 0, "End slot")
	clientName := flags.String("client", "", "Only use this specific client from config")
	concurrency := flags.Int("concurrency", 1, "Number of concurrent slot processors")
	flags.Parse(os.Args[1:])

	if *configPath == "" {
		fmt.Println("Error: config parameter is required")
		os.Exit(1)
	}

	if *endSlot < *startSlot {
		fmt.Println("Error: end slot must be greater than start slot")
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
	logger.SetLevel(logrus.InfoLevel)

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

	logger.Infof("Starting sync from slot %d to %d", *startSlot, *endSlot)

	// Create channels for work distribution and synchronization
	jobs := make(chan uint64, *concurrency)
	results := make(chan error, *concurrency)
	done := make(chan bool)

	// Start worker goroutines
	for i := 0; i < *concurrency; i++ {
		go func() {
			for slot := range jobs {
				err := processSlot(ctx, pool, dynSsz, slot, logger)
				results <- err
			}
		}()
	}

	// Start result collector
	go func() {
		for slot := *startSlot; slot < *endSlot; slot++ {
			err := <-results
			if err != nil {
				logger.Warn(err)
			}
		}
		done <- true
	}()

	// Send jobs
	for slot := *startSlot; slot < *endSlot; slot++ {
		jobs <- slot
	}
	close(jobs)

	// Wait for completion
	<-done

	logger.Info("Sync completed")
}

func processSlot(ctx context.Context, pool *consensus.Pool, dynSsz *dynssz.DynSsz, slot uint64, logger *logrus.Logger) error {
	client := pool.GetReadyEndpoint(consensus.AnyClient)
	if client == nil {
		return fmt.Errorf("no ready client found for slot %d", slot)
	}

	t1 := time.Now()
	log := logger.WithField("client", client.GetName())

	blockHeader, err := client.GetRPCClient().GetBlockHeaderBySlot(ctx, phase0.Slot(slot))
	if err != nil {
		return fmt.Errorf("failed to get block for slot %d: %v", slot, err)
	}

	if blockHeader == nil {
		log.Infof("Processed slot %d: missed  (%.2f ms)", slot, time.Since(t1).Seconds()*1000)
		return nil
	}

	// Store block header
	headerBytes, err := blockHeader.Header.MarshalSSZ()
	if err != nil {
		return fmt.Errorf("failed to marshal block header for slot %d: %v", slot, err)
	}

	added, err := blockdb.GlobalBlockDb.AddBlockHeader(blockHeader.Root[:], 1, headerBytes)
	if err != nil {
		return fmt.Errorf("failed to store block header for slot %d: %v", slot, err)
	}

	if added {
		// Store block body only if header was newly added
		blockBody, err := client.GetRPCClient().GetBlockBodyByBlockroot(ctx, blockHeader.Root)
		if err != nil {
			return fmt.Errorf("failed to get block body for slot %d: %v", slot, err)
		}

		version, bodyBytes, err := beacon.MarshalVersionedSignedBeaconBlockSSZ(dynSsz, blockBody, true, false)
		if err != nil {
			return fmt.Errorf("failed to marshal block body for slot %d: %v", slot, err)
		}

		err = blockdb.GlobalBlockDb.AddBlockBody(blockHeader.Root[:], version, bodyBytes)
		if err != nil {
			return fmt.Errorf("failed to store block body for slot %d: %v", slot, err)
		}

		log.Infof("Processed slot %d: added   (%.2f ms)", slot, time.Since(t1).Seconds()*1000)
	} else {
		log.Infof("Processed slot %d: present (%.2f ms)", slot, time.Since(t1).Seconds()*1000)
	}

	return nil
}
