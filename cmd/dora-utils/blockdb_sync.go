package main

import (
	"context"
	"fmt"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/blockdb"
	btypes "github.com/ethpandaops/dora/blockdb/types"
	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/clients/sshtunnel"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/types"
	"github.com/ethpandaops/dora/utils"
	dynssz "github.com/pk910/dynamic-ssz"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var blockdbSyncCmd = &cobra.Command{
	Use:   "blockdb-sync",
	Short: "Sync blocks from beacon node to blockdb",
	Long:  "Synchronize block data from beacon node to the block database for the specified epoch range",
	RunE:  runBlockdbSync,
}

func init() {
	rootCmd.AddCommand(blockdbSyncCmd)

	blockdbSyncCmd.Flags().StringP("config", "c", "", "Path to the config file (required)")
	blockdbSyncCmd.Flags().Uint64P("start", "s", 0, "Start epoch")
	blockdbSyncCmd.Flags().Uint64P("end", "e", 0, "End epoch")
	blockdbSyncCmd.Flags().String("client", "", "Only use this specific client from config")
	blockdbSyncCmd.Flags().IntP("concurrency", "j", 1, "Number of concurrent slot processors")
	blockdbSyncCmd.Flags().BoolP("verbose", "v", false, "Verbose output")

	blockdbSyncCmd.MarkFlagRequired("config")
}

func runBlockdbSync(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")
	startEpoch, _ := cmd.Flags().GetUint64("start")
	endEpoch, _ := cmd.Flags().GetUint64("end")
	clientName, _ := cmd.Flags().GetString("client")
	concurrency, _ := cmd.Flags().GetInt("concurrency")
	verbose, _ := cmd.Flags().GetBool("verbose")

	if endEpoch < startEpoch {
		return fmt.Errorf("end epoch must be greater than start epoch")
	}

	if concurrency < 1 {
		return fmt.Errorf("concurrency must be at least 1")
	}

	cfg := &types.Config{}
	err := utils.ReadConfig(cfg, configPath)
	if err != nil {
		return fmt.Errorf("error reading config file: %v", err)
	}
	utils.Config = cfg

	logger := logrus.New()
	if verbose {
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
	case "s3":
		err := blockdb.InitWithS3(cfg.BlockDb.S3)
		if err != nil {
			logger.Fatalf("Failed initializing s3 blockdb: %v", err)
		}
		logger.Infof("S3 blockdb initialized at %v", cfg.BlockDb.S3.Bucket)
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
		if clientName != "" && endpoint.Name != clientName {
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
		if clientName != "" {
			break
		}
	}

	if len(pool.GetAllEndpoints()) == 0 {
		if clientName != "" {
			logger.Fatalf("Client '%s' not found in config", clientName)
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
	startSlot := startEpoch * slotsPerEpoch
	endSlot := (endEpoch + 1) * slotsPerEpoch

	for {
		client := pool.GetReadyEndpoint(consensus.AnyClient)
		if client == nil {
			logger.Info("No ready client found, waiting for 10 seconds")
			time.Sleep(10 * time.Second)
			continue
		}

		break
	}

	logger.Infof("Starting sync from epoch %d to %d (slots %d to %d)", startEpoch, endEpoch, startSlot, endSlot-1)

	// Create channels for work distribution and synchronization
	jobs := make(chan uint64, concurrency)
	results := make(chan slotResult, concurrency)
	done := make(chan bool)

	// Initialize epoch stats map
	epochStats := make(map[uint64]*epochSummary)
	for epoch := startEpoch; epoch <= endEpoch; epoch++ {
		epochStats[epoch] = &epochSummary{
			processed: make(map[string]int),
		}
	}

	// Start worker goroutines
	for i := 0; i < concurrency; i++ {
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
	return nil
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

	added, _, err := blockdb.GlobalBlockDb.AddBlockWithCallback(ctx, slot, blockHeader.Root[:], func() (*btypes.BlockData, error) {
		blockBody, err := client.GetRPCClient().GetBlockBodyByBlockroot(ctx, blockHeader.Root)
		if err != nil {
			return nil, fmt.Errorf("failed to get block body for slot %d: %v", slot, err)
		}

		version, bodyBytes, err := beacon.MarshalVersionedSignedBeaconBlockSSZ(dynSsz, blockBody, true, false)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal block body for slot %d: %v", slot, err)
		}

		var payloadVersion uint64
		var payloadBytes []byte

		chainState := pool.GetChainState()
		if chainState.IsEip7732Enabled(chainState.EpochOfSlot(phase0.Slot(slot))) {
			blockPayload, err := client.GetRPCClient().GetExecutionPayloadByBlockroot(ctx, blockHeader.Root)
			if err != nil {
				return nil, fmt.Errorf("failed to get block execution payload for slot %d: %v", slot, err)
			}

			payloadVersion, payloadBytes, err = beacon.MarshalVersionedSignedExecutionPayloadEnvelopeSSZ(dynSsz, blockPayload, true)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal block execution payload for slot %d: %v", slot, err)
			}
		}

		return &btypes.BlockData{
			HeaderVersion:  1,
			HeaderData:     headerBytes,
			BodyVersion:    version,
			BodyData:       bodyBytes,
			PayloadVersion: payloadVersion,
			PayloadData:    payloadBytes,
		}, nil
	})
	if err != nil {
		return slotResult{slot: slot, err: fmt.Errorf("failed to store block header for slot %d: %v", slot, err), time: time.Since(t1)}
	}

	if added {
		log.Debugf("Slot %d: added   (%.2f ms)", slot, time.Since(t1).Seconds()*1000)
		return slotResult{slot: slot, status: "added", time: time.Since(t1)}
	} else {
		log.Debugf("Slot %d: present (%.2f ms)", slot, time.Since(t1).Seconds()*1000)
		return slotResult{slot: slot, status: "present", time: time.Since(t1)}
	}
}
