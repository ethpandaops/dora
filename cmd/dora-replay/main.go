package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/ethpandaops/dora/replay"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	cfg := replay.DefaultConfig()

	var (
		startEpoch uint64
		logLevel   string
		noBids     bool
	)

	cmd := &cobra.Command{
		Use:   "dora-replay",
		Short: "Replay a past slot range through Dora as if it were happening live",
		Long: "dora-replay serves a fake beacon/execution node pair backed by a real upstream and\n" +
			"drives a virtual clock, so an explorer pointed at it steps through a past slot range\n" +
			"with pause, step, seek and play. Point dora at the two listeners and set\n" +
			"`replay.enabled: true` with `replay.controlUrl` pointing at the control listener.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			logger := logrus.New()
			logger.SetOutput(os.Stderr)

			level, err := logrus.ParseLevel(logLevel)
			if err != nil {
				return fmt.Errorf("invalid log level %q: %w", logLevel, err)
			}

			logger.SetLevel(level)

			if startEpoch > 0 && cfg.StartSlot == 0 {
				slotsPerEpoch, err := replay.SlotsPerEpoch(cmd.Context(), cfg.UpstreamURL)
				if err != nil {
					return err
				}

				cfg.StartSlot = startEpoch * slotsPerEpoch
			}

			cfg.EmitBids = !noBids

			return run(cmd.Context(), logger, cfg)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&cfg.UpstreamURL, "upstream", "", "beacon node HTTP API to read the chain from (required, must serve historical states)")
	flags.StringVar(&cfg.ExecutionURL, "el-upstream", "", "execution node JSON-RPC to read from; omit to run without an execution proxy")
	flags.StringVar(&cfg.TracoorURL, "tracoor", "", "tracoor instance used as a fallback for artifacts the beacon node has pruned")
	flags.StringVar(&cfg.TracoorNetwork, "tracoor-network", "", "network name to query tracoor with")
	flags.StringVar(&cfg.CLListen, "cl-listen", cfg.CLListen, "listen address of the fake beacon node")
	flags.StringVar(&cfg.ELListen, "el-listen", cfg.ELListen, "listen address of the fake execution node")
	flags.StringVar(&cfg.ControlListen, "control-listen", cfg.ControlListen, "listen address of the control endpoint dora polls the clock from")
	flags.Uint64Var(&cfg.StartSlot, "start-slot", 0, "first slot to serve")
	flags.Uint64Var(&startEpoch, "start-epoch", 0, "first epoch to serve (alternative to --start-slot)")
	flags.Float64Var(&cfg.Speed, "speed", 0, "start playing at this multiple of real time; 0 starts paused")
	flags.StringVar(&cfg.CacheDir, "cache-dir", "", "record fetched artifacts here so the range replays offline afterwards")
	flags.DurationVar(&cfg.StepSettle, "step-settle", cfg.StepSettle, "real time to pause at each phase of a stepped slot")
	flags.DurationVar(&cfg.StateHoldTimeout, "state-hold-timeout", cfg.StateHoldTimeout, "how long to freeze the clock waiting for the explorer to load a beacon state")
	flags.BoolVar(&noBids, "no-bids", false, "do not replay execution payload bids (saves one block fetch per slot)")
	flags.StringVar(&logLevel, "log-level", "info", "log level (trace, debug, info, warn, error)")

	return cmd
}

func run(ctx context.Context, logger logrus.FieldLogger, cfg replay.Config) error {
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	instance, err := replay.New(ctx, logger, cfg)
	if err != nil {
		return err
	}

	if err := instance.Start(ctx); err != nil {
		return err
	}

	in, out := replay.Stdio()
	instance.RunConsole(ctx, in, out)

	if err := instance.Stop(); err != nil {
		logger.WithError(err).Warn("error shutting down replay")
	}

	return nil
}
