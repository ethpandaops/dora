// Package xatu provides read access to a Xatu ClickHouse instance using the
// typed query builders and row structs generated in the xatu repository.
package xatu

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/url"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/types"
)

const (
	defaultDatabase = "default"
	// maxQueryPageSize is the page_size ceiling the generated builders enforce.
	maxQueryPageSize = 10000
	// maxQueryPages bounds a paged read so a bad filter cannot walk a table
	// forever. The widest caller reduces one epoch of a single event series, so
	// 10 pages allows 100k rows there, about 3000 observing nodes per slot
	// across 32 slots. Exceeding it fails loudly rather than truncating,
	// because a short read yields plausible-looking wrong percentiles.
	maxQueryPages           = 10
	defaultSettleDelay      = 30 * time.Second
	defaultConcurrencyLimit = 2
	// cbtSettleDelay is how long after slot start the cbt transformations are
	// assumed to still be rewriting a slot's rows. The attestation chunk window
	// extends 12s past slot start, availability probes run within the probed
	// slot itself, and the transformation batches lag under a minute, so two
	// minutes covers all three with room.
	cbtSettleDelay = 2 * time.Minute
)

// GlobalClient is the process-wide xatu client. It is nil when xatu is not
// configured; all xatu-backed features must treat that as disabled.
var GlobalClient *Client

// GlobalCbtClient is the process-wide xatu-cbt client. It is nil when no cbt
// source is configured; all cbt-backed features must treat that as disabled.
var GlobalCbtClient *Client

// Client wraps ClickHouse connections to a Xatu instance. Queries for settled
// slots are routed to the cached endpoint when one is configured, so a
// response-caching proxy can absorb repeated queries across instances.
type Client struct {
	logger      *logrus.Entry
	conn        driver.Conn
	cachedConn  driver.Conn
	network     string
	settleDelay time.Duration
	sem         chan struct{}
}

// NewClient connects to the configured raw ClickHouse endpoints.
// defaultNetwork is used as the meta_network_name filter when the config does
// not set one.
func NewClient(cfg *types.XatuConfig, defaultNetwork string, logger logrus.FieldLogger) (*Client, error) {
	network := cfg.NetworkName
	if network == "" {
		network = defaultNetwork
	}

	settleDelay := cfg.SettleDelay
	if settleDelay <= 0 {
		settleDelay = defaultSettleDelay
	}

	return newClient(clientParams{
		module:      "xatu",
		dsn:         cfg.Raw.ClickhouseDsn,
		cachedDsn:   cfg.Raw.ClickhouseCachedDsn,
		database:    cfg.Raw.Database,
		network:     network,
		settleDelay: settleDelay,
		concurrency: cfg.ConcurrencyLimit,
	}, logger)
}

// NewCbtClient connects to the configured xatu-cbt ClickHouse endpoints. The
// cbt models live in one database per network, so the database defaults to
// the network name instead of a fixed schema. The settle delay is fixed: it
// covers the transformation pipeline, not the raw ingest lag the configured
// SettleDelay describes.
func NewCbtClient(cfg *types.XatuConfig, defaultNetwork string, logger logrus.FieldLogger) (*Client, error) {
	network := cfg.NetworkName
	if network == "" {
		network = defaultNetwork
	}

	database := cfg.Cbt.Database
	if database == "" {
		database = network
	}

	return newClient(clientParams{
		module:      "xatu-cbt",
		dsn:         cfg.Cbt.ClickhouseDsn,
		cachedDsn:   cfg.Cbt.ClickhouseCachedDsn,
		database:    database,
		network:     network,
		settleDelay: cbtSettleDelay,
		concurrency: cfg.ConcurrencyLimit,
	}, logger)
}

// clientParams carries one source's resolved connection settings into
// newClient.
type clientParams struct {
	module      string
	dsn         string
	cachedDsn   string
	database    string
	network     string
	settleDelay time.Duration
	concurrency int
}

func newClient(params clientParams, logger logrus.FieldLogger) (*Client, error) {
	if params.dsn == "" {
		return nil, fmt.Errorf("%s clickhouse dsn is required", params.module)
	}

	conn, err := connect(params.dsn, params.database)
	if err != nil {
		return nil, fmt.Errorf("%s clickhouse: %w", params.module, err)
	}

	client := &Client{
		logger:      logger.WithField("module", params.module),
		conn:        conn,
		network:     params.network,
		settleDelay: params.settleDelay,
	}

	if params.cachedDsn != "" {
		cachedConn, err := connect(params.cachedDsn, params.database)
		if err != nil {
			return nil, fmt.Errorf("%s cached clickhouse: %w", params.module, err)
		}

		client.cachedConn = cachedConn
	}

	concurrency := params.concurrency
	if concurrency <= 0 {
		concurrency = defaultConcurrencyLimit
	}

	client.sem = make(chan struct{}, concurrency)

	go client.logReachability()

	return client, nil
}

// logReachability pings the configured endpoints once and logs the outcome.
// A failure is logged rather than returned: queries recover on their own when
// ClickHouse comes back, so refusing to boot would turn an outage in an
// optional dependency into downtime for the whole explorer.
func (c *Client) logReachability() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	endpoints := []struct {
		name string
		conn driver.Conn
	}{
		{"primary", c.conn},
		{"cached", c.cachedConn},
	}

	for _, endpoint := range endpoints {
		if endpoint.conn == nil {
			continue
		}

		if err := endpoint.conn.Ping(ctx); err != nil {
			c.logger.WithError(err).Errorf("xatu clickhouse %s endpoint unreachable, propagation data stays unavailable until it recovers", endpoint.name)

			continue
		}

		c.logger.Infof("xatu clickhouse %s endpoint reachable", endpoint.name)
	}
}

// Network returns the meta_network_name filter value.
func (c *Client) Network() string {
	return c.network
}

// SettleDelay returns how long after slot start the ingest pipeline is assumed
// to still be receiving events for that slot.
func (c *Client) SettleDelay() time.Duration {
	return c.settleDelay
}

// Query runs a built query. Settled queries use the cached endpoint when one
// is configured; unsettled queries always bypass it so a pre-settle response
// never gets frozen into a shared response cache.
func (c *Client) Query(ctx context.Context, settled bool, query string, args ...any) (driver.Rows, error) {
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-c.sem }()

	conn := c.conn
	if settled && c.cachedConn != nil {
		conn = c.cachedConn
	}

	return conn.Query(ctx, query, args...)
}

// QueryPaged runs a query across every result page, calling scan for each row.
//
// The generated builders cap page_size at maxQueryPageSize and cannot express
// aggregates, so a caller reducing a whole epoch has to pull the rows and
// follow the page tokens. Reading only the first page loses rows silently,
// which is worse than being slow. build receives the row offset of the page
// to fetch, zero for the first; it encodes the offset into a page token with
// its own table's generated helper, which keeps this client independent of
// which generated package (xatu or xatu-cbt) produced the query.
func (c *Client) QueryPaged(
	ctx context.Context,
	settled bool,
	build func(pageOffset uint32) (query string, args []any, err error),
	scan func(rows driver.Rows) error,
) error {
	offset := uint32(0)

	for page := 0; page < maxQueryPages; page++ {
		query, args, err := build(offset)
		if err != nil {
			return err
		}

		rows, err := c.Query(ctx, settled, query, args...)
		if err != nil {
			return err
		}

		count := 0

		for rows.Next() {
			count++

			if err := scan(rows); err != nil {
				rows.Close()

				return err
			}
		}

		// a mid-stream failure would otherwise look like a short final page
		if err := rows.Err(); err != nil {
			rows.Close()

			return err
		}

		rows.Close()

		if count < maxQueryPageSize {
			return nil
		}

		offset += maxQueryPageSize
	}

	return fmt.Errorf("query exceeded %d pages of %d rows", maxQueryPages, maxQueryPageSize)
}

// MaxQueryPageSize is the page size callers should request so QueryPaged can
// tell a full page from the last one.
func MaxQueryPageSize() int32 {
	return maxQueryPageSize
}

// connect opens a ClickHouse connection from a DSN. https/http DSNs use the
// HTTP protocol (chproxy compatible), clickhouse:// DSNs use the native
// protocol.
func connect(dsn, database string) (driver.Conn, error) {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("invalid dsn: %w", err)
	}

	if database == "" {
		database = defaultDatabase
	}

	options := &clickhouse.Options{
		Auth: clickhouse.Auth{
			Database: database,
			Username: parsed.User.Username(),
		},
		DialTimeout: 10 * time.Second,
		ReadTimeout: 60 * time.Second,
		// sized above the per-client concurrency limit: the default pool ran
		// dry once a slot view fanned out to several series, and an exhausted
		// pool surfaces as an acquire timeout that reads like an outage
		MaxOpenConns:    8,
		MaxIdleConns:    4,
		ConnMaxLifetime: time.Hour,
	}

	if password, ok := parsed.User.Password(); ok {
		options.Auth.Password = password
	}

	host := parsed.Hostname()
	port := parsed.Port()

	switch parsed.Scheme {
	case "https":
		if port == "" {
			port = "443"
		}

		options.Protocol = clickhouse.HTTP
		options.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	case "http":
		if port == "" {
			port = "8123"
		}

		options.Protocol = clickhouse.HTTP
	case "clickhouse":
		if port == "" {
			port = "9000"
		}

		options.Protocol = clickhouse.Native
	default:
		return nil, fmt.Errorf("unsupported dsn scheme %q", parsed.Scheme)
	}

	options.Addr = []string{host + ":" + port}

	// Open validates the options without dialing. Reachability is checked in
	// the background so an unreachable ClickHouse cannot stop dora booting.
	return clickhouse.Open(options)
}
