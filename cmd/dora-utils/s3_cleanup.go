package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"text/tabwriter"
	"time"

	"github.com/kelseyhightower/envconfig"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/minio/minio-go/v7/pkg/lifecycle"
	"github.com/minio/minio-go/v7/pkg/signer"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	dtypes "github.com/ethpandaops/dora/types"
)

// cleanupRuleIDPrefix namespaces the lifecycle rules created by this tool so we
// never touch unrelated (e.g. bucket-wide) rules already present on the bucket.
const cleanupRuleIDPrefix = "dora-cleanup-"

// defaultDomainTemplate maps a devnet prefix name to the dora URL used to probe
// whether the devnet is still alive. %s is replaced with the prefix name.
const defaultDomainTemplate = "https://dora.%s.ethpandaops.io"

// defaultDoraMarker is searched for in the probe response body. A devnet is only
// considered active when it returns HTTP 200 AND serves a real dora (this string
// in the page) - a torn-down devnet still answers 404/503 from the ingress.
const defaultDoraMarker = "Dora the Explorer"

// defaultDevnetPattern matches the feature-devnet-n naming scheme. Anything that
// does not match (and is not in the keep list) is left untouched.
const defaultDevnetPattern = `^.+-devnet-[0-9]+$`

// defaultKeepPrefixes are static networks that must never be cleaned up.
var defaultKeepPrefixes = []string{"mainnet", "sepolia", "hoodi"}

// maxQuickSample bounds the per-prefix object listing used for display so we
// never block on a full recursive scan of a huge prefix.
const maxQuickSample = 1000

type prefixClass int

const (
	classKeepStatic prefixClass = iota
	classActiveDevnet
	classInactiveDevnet
	classUnknown
)

func (c prefixClass) String() string {
	switch c {
	case classKeepStatic:
		return "keep (static)"
	case classActiveDevnet:
		return "keep (active)"
	case classInactiveDevnet:
		return "INACTIVE"
	default:
		return "keep (unknown)"
	}
}

// prefixInfo holds the classification result for one top-level prefix.
type prefixInfo struct {
	Name   string
	Class  prefixClass
	URL    string
	Status string // probe outcome, human readable
}

// s3-cleanup walks the bucket, classifies prefixes and schedules cleanup of
// inactive devnets. Policy management (inspect/undo/prune the rules it creates)
// lives in the separate s3-policy command.
var s3CleanupCmd = &cobra.Command{
	Use:   "s3-cleanup",
	Short: "Classify shared-bucket prefixes and clean up inactive devnets",
	Long: `Walk every top-level prefix of a shared dora S3 bucket and decide, per
prefix, whether the devnet it belongs to is still in use.

Static networks (mainnet, sepolia, hoodi) are never touched. Devnet prefixes
matching the feature-devnet-n pattern are probed via their dora URL
(https://dora.<prefix>.ethpandaops.io); a prefix is only kept when it serves a
live dora (HTTP 200 with the dora page marker). Everything else is an
inactive-cleanup candidate, confirmed per-prefix before any change.

Cleanup defaults to a server-side lifecycle rule (prefix-filtered expiration)
instead of deleting objects one by one. Inspect, undo or prune those rules with
the s3-policy command.

Example:
  dora-utils s3-cleanup -c config.yaml --dry-run
  dora-utils s3-cleanup -c config.yaml
  dora-utils s3-cleanup -c config.yaml --yes --direct-delete`,
	RunE: runS3CleanupScan,
}

// s3-policy manages the lifecycle rules created by s3-cleanup.
var s3PolicyCmd = &cobra.Command{
	Use:   "s3-policy",
	Short: "Manage the cleanup lifecycle rules on the shared S3 bucket",
	Long: `Inspect and manage the prefix-expiration lifecycle rules that s3-cleanup
creates. Use 'list' to see scheduled cleanups, 'cancel' to undo one (e.g. a
revived devnet), and 'prune' to drop rules whose prefix has already drained.`,
}

func init() {
	rootCmd.AddCommand(s3CleanupCmd)
	rootCmd.AddCommand(s3PolicyCmd)

	// s3-cleanup (flat scan command).
	s3CleanupCmd.Flags().StringP("config", "c", "", "Path to a config file with blockDb.s3 settings (required)")
	s3CleanupCmd.Flags().BoolP("verbose", "v", false, "Verbose output")
	s3CleanupCmd.Flags().Bool("dry-run", false, "Only classify and report; never prompt or modify anything")
	s3CleanupCmd.Flags().BoolP("yes", "y", false, "Auto-confirm cleanup for every inactive devnet (unattended)")
	s3CleanupCmd.Flags().Bool("direct-delete", false, "Delete objects directly instead of adding a lifecycle expiration rule")
	s3CleanupCmd.Flags().Int("expire-days", 1, "Days after which the lifecycle rule expires objects under a prefix")
	s3CleanupCmd.Flags().String("domain", defaultDomainTemplate, "URL template for the dora probe (%s = prefix name)")
	s3CleanupCmd.Flags().String("marker", defaultDoraMarker, "Body marker that proves a live dora is served")
	s3CleanupCmd.Flags().String("devnet-pattern", defaultDevnetPattern, "Regex identifying devnet prefixes")
	s3CleanupCmd.Flags().StringSlice("keep", nil, "Additional static prefixes to never clean up (merged with defaults)")
	s3CleanupCmd.Flags().Duration("http-timeout", 10*time.Second, "Per-probe HTTP timeout")
	if err := s3CleanupCmd.MarkFlagRequired("config"); err != nil {
		panic(err)
	}

	// s3-policy (shared creds via persistent flags, inherited by subcommands).
	s3PolicyCmd.PersistentFlags().StringP("config", "c", "", "Path to a config file with blockDb.s3 settings (required)")
	s3PolicyCmd.PersistentFlags().BoolP("verbose", "v", false, "Verbose output")
	if err := s3PolicyCmd.MarkPersistentFlagRequired("config"); err != nil {
		panic(err)
	}

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List the cleanup lifecycle rules currently set on the bucket",
		RunE:  runS3PolicyList,
	}
	s3PolicyCmd.AddCommand(listCmd)

	cancelCmd := &cobra.Command{
		Use:   "cancel <prefix> [prefix...]",
		Short: "Remove cleanup lifecycle rules for the given prefixes (undo a scheduled cleanup)",
		Args:  cobra.MinimumNArgs(1),
		RunE:  runS3PolicyCancel,
	}
	s3PolicyCmd.AddCommand(cancelCmd)

	pruneCmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove cleanup rules whose prefix has already drained (no objects left)",
		RunE:  runS3PolicyPrune,
	}
	pruneCmd.Flags().Bool("dry-run", false, "Only report which rules would be pruned")
	pruneCmd.Flags().BoolP("yes", "y", false, "Auto-confirm pruning")
	s3PolicyCmd.AddCommand(pruneCmd)
}

// s3CleanupFileConfig is the minimal slice of the dora config we need: just the
// blockDb.s3 credentials/bucket. We read it directly (instead of utils.ReadConfig)
// so a blockDb-only settings file without beacon endpoints is accepted.
type s3CleanupFileConfig struct {
	BlockDb struct {
		S3 dtypes.S3BlockDBConfig `yaml:"s3"`
	} `yaml:"blockDb"`
}

func loadS3CleanupConfig(path string) (dtypes.S3BlockDBConfig, error) {
	var fileCfg s3CleanupFileConfig

	data, err := os.ReadFile(path)
	if err != nil {
		return dtypes.S3BlockDBConfig{}, fmt.Errorf("error reading config file: %w", err)
	}
	if err := yaml.Unmarshal(data, &fileCfg); err != nil {
		return dtypes.S3BlockDBConfig{}, fmt.Errorf("error parsing config file: %w", err)
	}

	// Allow BLOCKDB_S3_* env vars to override file values (matches main config).
	if err := envconfig.Process("", &fileCfg.BlockDb.S3); err != nil {
		return dtypes.S3BlockDBConfig{}, fmt.Errorf("error processing env config: %w", err)
	}

	s3Cfg := fileCfg.BlockDb.S3
	if s3Cfg.Endpoint == "" {
		return dtypes.S3BlockDBConfig{}, fmt.Errorf("blockDb.s3.endpoint is required")
	}
	if s3Cfg.Bucket == "" {
		return dtypes.S3BlockDBConfig{}, fmt.Errorf("blockDb.s3.bucket is required")
	}

	return s3Cfg, nil
}

// s3CleanupSetup loads the config, builds the client and verifies the bucket.
func s3CleanupSetup(cmd *cobra.Command) (*minio.Client, dtypes.S3BlockDBConfig, *logrus.Logger, error) {
	configPath, _ := cmd.Flags().GetString("config")
	verbose, _ := cmd.Flags().GetBool("verbose")

	logger := logrus.New()
	if verbose {
		logger.SetLevel(logrus.DebugLevel)
	} else {
		logger.SetLevel(logrus.InfoLevel)
	}

	s3Cfg, err := loadS3CleanupConfig(configPath)
	if err != nil {
		return nil, s3Cfg, nil, err
	}

	client, err := minio.New(s3Cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(s3Cfg.AccessKey, s3Cfg.SecretKey, ""),
		Secure: bool(s3Cfg.Secure),
		Region: s3Cfg.Region,
	})
	if err != nil {
		return nil, s3Cfg, nil, fmt.Errorf("failed to create S3 client: %w", err)
	}

	exists, err := client.BucketExists(cmd.Context(), s3Cfg.Bucket)
	if err != nil {
		return nil, s3Cfg, nil, fmt.Errorf("failed to check bucket: %w", err)
	}
	if !exists {
		return nil, s3Cfg, nil, fmt.Errorf("bucket %q does not exist", s3Cfg.Bucket)
	}

	logger.WithFields(logrus.Fields{
		"bucket":   s3Cfg.Bucket,
		"endpoint": s3Cfg.Endpoint,
	}).Debug("connected to S3")

	return client, s3Cfg, logger, nil
}

func runS3CleanupScan(cmd *cobra.Command, _ []string) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Minute)
	defer cancel()

	dryRun, _ := cmd.Flags().GetBool("dry-run")
	autoYes, _ := cmd.Flags().GetBool("yes")
	directDelete, _ := cmd.Flags().GetBool("direct-delete")
	expireDays, _ := cmd.Flags().GetInt("expire-days")
	domainTpl, _ := cmd.Flags().GetString("domain")
	marker, _ := cmd.Flags().GetString("marker")
	pattern, _ := cmd.Flags().GetString("devnet-pattern")
	extraKeep, _ := cmd.Flags().GetStringSlice("keep")
	httpTimeout, _ := cmd.Flags().GetDuration("http-timeout")

	devnetRe, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("invalid devnet-pattern: %w", err)
	}
	if expireDays < 1 {
		return fmt.Errorf("expire-days must be >= 1")
	}

	client, s3Cfg, logger, err := s3CleanupSetup(cmd)
	if err != nil {
		return err
	}
	bucket := s3Cfg.Bucket

	keep := keepSet(extraKeep)

	prefixes, err := listTopPrefixes(ctx, client, bucket)
	if err != nil {
		return err
	}
	logger.WithField("count", len(prefixes)).Info("discovered top-level prefixes")

	infos := classifyPrefixes(ctx, prefixes, keep, devnetRe, domainTpl, marker, httpTimeout)
	printClassification(infos)

	candidates := make([]prefixInfo, 0)
	for _, info := range infos {
		if info.Class == classInactiveDevnet {
			candidates = append(candidates, info)
		}
	}

	if len(candidates) == 0 {
		logger.Info("no inactive devnets to clean up")
		return nil
	}

	mode := "lifecycle expiration rule"
	if directDelete {
		mode = "direct delete"
	}
	logger.WithFields(logrus.Fields{
		"candidates": len(candidates),
		"mode":       mode,
	}).Info("inactive devnets found")

	if dryRun {
		logger.Info("dry-run: no changes made")
		return nil
	}

	reader := bufio.NewReader(os.Stdin)
	confirmed := make([]prefixInfo, 0, len(candidates))
	for _, c := range candidates {
		count, truncated, err := quickSample(ctx, client, bucket, c.Name)
		sizeStr := describeSample(count, truncated, err)

		if autoYes {
			logger.WithFields(logrus.Fields{"prefix": c.Name, "objects": sizeStr}).Info("auto-confirmed cleanup")
			confirmed = append(confirmed, c)
			continue
		}

		prompt := fmt.Sprintf("Clean up %q (%s, ~%s)? [y/N]: ", c.Name, c.Status, sizeStr)
		if confirm(reader, prompt) {
			confirmed = append(confirmed, c)
		} else {
			logger.WithField("prefix", c.Name).Info("skipped")
		}
	}

	if len(confirmed) == 0 {
		logger.Info("nothing confirmed; no changes made")
		return nil
	}

	if directDelete {
		return directDeletePrefixes(ctx, logger, client, bucket, confirmed)
	}
	return scheduleLifecycleCleanup(ctx, logger, client, s3Cfg, confirmed, expireDays)
}

// keepSet merges the default static networks with any user-provided extras.
func keepSet(extra []string) map[string]bool {
	keep := make(map[string]bool, len(defaultKeepPrefixes)+len(extra))
	for _, k := range defaultKeepPrefixes {
		keep[k] = true
	}
	for _, k := range extra {
		k = strings.TrimSuffix(strings.TrimSpace(k), "/")
		if k != "" {
			keep[k] = true
		}
	}
	return keep
}

// listTopPrefixes returns the top-level "folder" names (without trailing slash).
func listTopPrefixes(ctx context.Context, client *minio.Client, bucket string) ([]string, error) {
	prefixes := make([]string, 0)
	for obj := range client.ListObjects(ctx, bucket, minio.ListObjectsOptions{Recursive: false}) {
		if obj.Err != nil {
			return nil, fmt.Errorf("error listing prefixes: %w", obj.Err)
		}
		// Common prefixes arrive as keys ending in "/"; bare objects are ignored.
		if strings.HasSuffix(obj.Key, "/") {
			prefixes = append(prefixes, strings.TrimSuffix(obj.Key, "/"))
		}
	}
	sort.Strings(prefixes)
	return prefixes, nil
}

// classifyPrefixes classifies every prefix, probing devnets concurrently.
func classifyPrefixes(
	ctx context.Context,
	prefixes []string,
	keep map[string]bool,
	devnetRe *regexp.Regexp,
	domainTpl, marker string,
	httpTimeout time.Duration,
) []prefixInfo {
	infos := make([]prefixInfo, len(prefixes))

	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)

	for i, name := range prefixes {
		info := prefixInfo{Name: name}

		switch {
		case keep[name]:
			info.Class = classKeepStatic
			info.Status = "static network"
			infos[i] = info
		case !devnetRe.MatchString(name):
			info.Class = classUnknown
			info.Status = "does not match devnet pattern"
			infos[i] = info
		default:
			info.URL = fmt.Sprintf(domainTpl, name)
			wg.Add(1)
			go func(idx int, info prefixInfo) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				active, status := probeDevnet(ctx, info.URL, marker, httpTimeout)
				if active {
					info.Class = classActiveDevnet
				} else {
					info.Class = classInactiveDevnet
				}
				info.Status = status
				infos[idx] = info
			}(i, info)
		}
	}

	wg.Wait()
	return infos
}

// probeDevnet fetches the dora URL and reports whether a live dora is served.
// Active requires HTTP 200 plus the dora page marker - a torn-down devnet still
// answers 404/503 from the ingress, so the status code alone is not enough.
func probeDevnet(ctx context.Context, url, marker string, timeout time.Duration) (bool, string) {
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return false, fmt.Sprintf("request error: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, "unreachable (DNS/connection failed)"
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Sprintf("HTTP %d (no live dora)", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return false, fmt.Sprintf("HTTP 200 but read error: %v", err)
	}
	if !strings.Contains(string(body), marker) {
		return false, "HTTP 200 but not a dora page"
	}

	return true, "HTTP 200, live dora"
}

func printClassification(infos []prefixInfo) {
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "PREFIX\tCLASS\tDETAIL")
	for _, info := range infos {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", info.Name, info.Class, info.Status)
	}
	tw.Flush()
	fmt.Println()
}

// scheduleLifecycleCleanup merges prefix-filtered expiration rules into the
// bucket's existing lifecycle configuration (preserving all other rules) and
// writes it back in a single request.
func scheduleLifecycleCleanup(
	ctx context.Context,
	logger *logrus.Logger,
	client *minio.Client,
	s3Cfg dtypes.S3BlockDBConfig,
	confirmed []prefixInfo,
	expireDays int,
) error {
	bucket := s3Cfg.Bucket
	cfg, err := getLifecycle(ctx, client, bucket)
	if err != nil {
		return err
	}

	existing := make(map[string]bool, len(cfg.Rules))
	for _, r := range cfg.Rules {
		existing[r.ID] = true
	}

	added := 0
	for _, c := range confirmed {
		ruleID := cleanupRuleIDPrefix + c.Name
		if existing[ruleID] {
			logger.WithField("prefix", c.Name).Info("lifecycle rule already present; skipping")
			continue
		}

		cfg.Rules = append(cfg.Rules, lifecycle.Rule{
			ID:         ruleID,
			Status:     "Enabled",
			RuleFilter: lifecycle.Filter{Prefix: c.Name + "/"},
			Expiration: lifecycle.Expiration{
				Days: lifecycle.ExpirationDays(expireDays),
			},
			NoncurrentVersionExpiration: lifecycle.NoncurrentVersionExpiration{
				NoncurrentDays: lifecycle.ExpirationDays(expireDays),
			},
		})
		added++
		logger.WithFields(logrus.Fields{
			"prefix": c.Name,
			"ruleID": ruleID,
			"days":   expireDays,
		}).Info("scheduled lifecycle expiration")
	}

	if added == 0 {
		logger.Info("no new lifecycle rules to apply")
		return nil
	}

	if err := putBucketLifecycle(ctx, client, s3Cfg, cfg); err != nil {
		return err
	}

	logger.WithField("added", added).Info("lifecycle rules applied; objects will expire server-side")
	return nil
}

// directDeletePrefixes removes all object versions under each confirmed prefix.
func directDeletePrefixes(
	ctx context.Context,
	logger *logrus.Logger,
	client *minio.Client,
	bucket string,
	confirmed []prefixInfo,
) error {
	for _, c := range confirmed {
		log := logger.WithField("prefix", c.Name)
		log.Info("deleting objects (including versions)")

		objCh := client.ListObjects(ctx, bucket, minio.ListObjectsOptions{
			Prefix:       c.Name + "/",
			Recursive:    true,
			WithVersions: true,
		})

		deleteCh := make(chan minio.ObjectInfo, 1000)
		var queued atomic.Int64
		var listErr error
		go func() {
			defer close(deleteCh)
			for obj := range objCh {
				if obj.Err != nil {
					listErr = fmt.Errorf("error listing objects: %w", obj.Err)
					return
				}
				deleteCh <- obj
				queued.Add(1)
			}
		}()

		// RemoveObjects only emits failures on its channel; successes are silent.
		// Count actual deletions as queued-minus-failed.
		var failed int64
		for rerr := range client.RemoveObjects(ctx, bucket, deleteCh, minio.RemoveObjectsOptions{}) {
			if rerr.Err != nil {
				log.WithError(rerr.Err).WithField("key", rerr.ObjectName).Warn("failed to delete object")
				failed++
			}
		}
		if listErr != nil {
			return listErr
		}

		log.WithFields(logrus.Fields{
			"deleted": queued.Load() - failed,
			"failed":  failed,
		}).Info("prefix cleared")
	}
	return nil
}

func runS3PolicyList(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	client, s3Cfg, logger, err := s3CleanupSetup(cmd)
	if err != nil {
		return err
	}
	bucket := s3Cfg.Bucket

	cfg, err := getLifecycle(ctx, client, bucket)
	if err != nil {
		return err
	}

	cleanupRules := make([]lifecycle.Rule, 0)
	otherRules := make([]string, 0)
	for _, r := range cfg.Rules {
		if strings.HasPrefix(r.ID, cleanupRuleIDPrefix) {
			cleanupRules = append(cleanupRules, r)
		} else {
			otherRules = append(otherRules, r.ID)
		}
	}

	if len(otherRules) > 0 {
		logger.WithField("rules", strings.Join(otherRules, ", ")).Info("other (non-cleanup) lifecycle rules present")
	}

	if len(cleanupRules) == 0 {
		logger.Info("no cleanup lifecycle rules currently set")
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "RULE ID\tPREFIX\tEXPIRE DAYS\tSTATUS\tOBJECTS LEFT")
	for _, r := range cleanupRules {
		prefix := strings.TrimSuffix(r.RuleFilter.Prefix, "/")
		hasObjects, _ := prefixHasObjects(ctx, client, bucket, prefix)
		left := "no (drained)"
		if hasObjects {
			left = "yes"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n", r.ID, prefix, int(r.Expiration.Days), r.Status, left)
	}
	tw.Flush()
	return nil
}

func runS3PolicyCancel(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	client, s3Cfg, logger, err := s3CleanupSetup(cmd)
	if err != nil {
		return err
	}
	bucket := s3Cfg.Bucket

	// Accept either a bare prefix name or a full rule ID.
	wanted := make(map[string]bool, len(args))
	for _, a := range args {
		a = strings.TrimSuffix(strings.TrimSpace(a), "/")
		wanted[cleanupRuleIDPrefix+strings.TrimPrefix(a, cleanupRuleIDPrefix)] = true
	}

	cfg, err := getLifecycle(ctx, client, bucket)
	if err != nil {
		return err
	}

	kept := make([]lifecycle.Rule, 0, len(cfg.Rules))
	removed := 0
	for _, r := range cfg.Rules {
		if wanted[r.ID] {
			logger.WithField("ruleID", r.ID).Info("removing cleanup rule")
			removed++
			continue
		}
		kept = append(kept, r)
	}

	if removed == 0 {
		logger.Info("no matching cleanup rules found")
		return nil
	}

	cfg.Rules = kept
	if err := putBucketLifecycle(ctx, client, s3Cfg, cfg); err != nil {
		return err
	}

	logger.WithField("removed", removed).Info("cleanup rules cancelled")
	return nil
}

func runS3PolicyPrune(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	autoYes, _ := cmd.Flags().GetBool("yes")

	client, s3Cfg, logger, err := s3CleanupSetup(cmd)
	if err != nil {
		return err
	}
	bucket := s3Cfg.Bucket

	cfg, err := getLifecycle(ctx, client, bucket)
	if err != nil {
		return err
	}

	drained := make(map[string]bool)
	for _, r := range cfg.Rules {
		if !strings.HasPrefix(r.ID, cleanupRuleIDPrefix) {
			continue
		}
		prefix := strings.TrimSuffix(r.RuleFilter.Prefix, "/")
		hasObjects, err := prefixHasObjects(ctx, client, bucket, prefix)
		if err != nil {
			return err
		}
		if !hasObjects {
			drained[r.ID] = true
			logger.WithFields(logrus.Fields{"ruleID": r.ID, "prefix": prefix}).Info("rule prefix drained")
		}
	}

	if len(drained) == 0 {
		logger.Info("no drained cleanup rules to prune")
		return nil
	}

	if dryRun {
		logger.WithField("count", len(drained)).Info("dry-run: rules that would be pruned")
		return nil
	}

	if !autoYes {
		reader := bufio.NewReader(os.Stdin)
		if !confirm(reader, fmt.Sprintf("Prune %d drained cleanup rule(s)? [y/N]: ", len(drained))) {
			logger.Info("aborted")
			return nil
		}
	}

	kept := make([]lifecycle.Rule, 0, len(cfg.Rules))
	for _, r := range cfg.Rules {
		if drained[r.ID] {
			continue
		}
		kept = append(kept, r)
	}
	cfg.Rules = kept

	if err := putBucketLifecycle(ctx, client, s3Cfg, cfg); err != nil {
		return err
	}

	logger.WithField("pruned", len(drained)).Info("drained cleanup rules removed")
	return nil
}

// putBucketLifecycle writes the lifecycle configuration back to the bucket.
//
// It does not use minio's SetBucketLifecycle directly: minio's XML marshaller
// omits the <Filter> element for match-all rules (an empty filter is treated as
// null), but S3 (e.g. OVH) requires every rule to carry a <Filter>. A rule with
// ExpiredObjectDeleteMarker may only carry an *empty* filter, which the struct
// cannot express. So we let minio marshal the document, inject an empty
// <Filter></Filter> into any rule that lacks one, and PUT the corrected body
// with a request signed by minio's own v4 signer.
func putBucketLifecycle(ctx context.Context, client *minio.Client, s3Cfg dtypes.S3BlockDBConfig, cfg *lifecycle.Configuration) error {
	bucket := s3Cfg.Bucket

	// An empty configuration removes the lifecycle entirely; minio handles that.
	if cfg == nil || len(cfg.Rules) == 0 {
		if err := client.SetBucketLifecycle(ctx, bucket, lifecycle.NewConfiguration()); err != nil {
			return fmt.Errorf("failed to remove bucket lifecycle: %w", err)
		}
		return nil
	}

	raw, err := xml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal lifecycle config: %w", err)
	}
	body := injectEmptyFilters(raw)

	region, err := client.GetBucketLocation(ctx, bucket)
	if err != nil {
		return fmt.Errorf("failed to resolve bucket region: %w", err)
	}
	if region == "" {
		region = "us-east-1"
	}

	scheme := "https"
	if !bool(s3Cfg.Secure) {
		scheme = "http"
	}
	url := fmt.Sprintf("%s://%s/%s?lifecycle=", scheme, s3Cfg.Endpoint, bucket)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to build lifecycle request: %w", err)
	}
	req.ContentLength = int64(len(body))

	// S3 requires a Content-MD5 on lifecycle PUTs, and the signer reads the
	// payload hash from X-Amz-Content-Sha256.
	sha := sha256.Sum256(body)
	req.Header.Set("X-Amz-Content-Sha256", hex.EncodeToString(sha[:]))
	sum := md5.Sum(body)
	req.Header.Set("Content-Md5", base64.StdEncoding.EncodeToString(sum[:]))

	signed := signer.SignV4(*req, s3Cfg.AccessKey, s3Cfg.SecretKey, "", region)

	resp, err := http.DefaultClient.Do(signed)
	if err != nil {
		return fmt.Errorf("lifecycle PUT failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("lifecycle PUT returned %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// injectEmptyFilters inserts an empty <Filter></Filter> into any <Rule> that was
// marshalled without a filter or prefix, making match-all rules valid for S3.
func injectEmptyFilters(marshalled []byte) []byte {
	const ruleEnd = "</Rule>"
	s := string(marshalled)

	var out strings.Builder
	for {
		i := strings.Index(s, ruleEnd)
		if i < 0 {
			out.WriteString(s)
			break
		}
		rule := s[:i]
		if !strings.Contains(rule, "<Filter") && !strings.Contains(rule, "<Prefix>") {
			rule += "<Filter></Filter>"
		}
		out.WriteString(rule)
		out.WriteString(ruleEnd)
		s = s[i+len(ruleEnd):]
	}
	return []byte(out.String())
}

// getLifecycle returns the bucket lifecycle config, or an empty config when the
// bucket has none set yet.
func getLifecycle(ctx context.Context, client *minio.Client, bucket string) (*lifecycle.Configuration, error) {
	cfg, err := client.GetBucketLifecycle(ctx, bucket)
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchLifecycleConfiguration" {
			return lifecycle.NewConfiguration(), nil
		}
		return nil, fmt.Errorf("failed to get bucket lifecycle: %w", err)
	}
	if cfg == nil {
		cfg = lifecycle.NewConfiguration()
	}
	return cfg, nil
}

// prefixHasObjects reports whether any object version exists under the prefix.
func prefixHasObjects(ctx context.Context, client *minio.Client, bucket, prefix string) (bool, error) {
	listCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Peek the first listed object: closed channel means the prefix is empty.
	obj, ok := <-client.ListObjects(listCtx, bucket, minio.ListObjectsOptions{
		Prefix:       prefix + "/",
		Recursive:    true,
		WithVersions: true,
		MaxKeys:      1,
	})
	if !ok {
		return false, nil
	}
	if obj.Err != nil {
		return false, fmt.Errorf("error listing %q: %w", prefix, obj.Err)
	}
	return true, nil
}

// quickSample counts up to maxQuickSample current objects under a prefix for a
// fast, bounded indication of how much data a cleanup would affect.
func quickSample(ctx context.Context, client *minio.Client, bucket, prefix string) (int, bool, error) {
	listCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	count := 0
	for obj := range client.ListObjects(listCtx, bucket, minio.ListObjectsOptions{
		Prefix:    prefix + "/",
		Recursive: true,
	}) {
		if obj.Err != nil {
			return count, false, fmt.Errorf("error listing %q: %w", prefix, obj.Err)
		}
		count++
		if count >= maxQuickSample {
			return count, true, nil
		}
	}
	return count, false, nil
}

func describeSample(count int, truncated bool, err error) string {
	if err != nil {
		return "size unknown"
	}
	if truncated {
		return fmt.Sprintf(">=%d objects", count)
	}
	return fmt.Sprintf("%d objects", count)
}

// confirm prompts on stdin and returns true only on an explicit yes.
func confirm(reader *bufio.Reader, prompt string) bool {
	fmt.Print(prompt)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}
