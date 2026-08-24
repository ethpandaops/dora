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
	xch "github.com/ethpandaops/xatu/pkg/proto/clickhouse"
)

const (
	defaultDatabase = "default"
	// maxQueryPageSize is the page_size ceiling the generated builders enforce.
	maxQueryPageSize = 10000
	// maxQueryPages bounds a paged read so a bad filter cannot walk a table
	// forever. 10 pages is far above any single epoch's event count.
	maxQueryPages           = 10
	defaultSettleDelay      = 30 * time.Second
	defaultConcurrencyLimit = 2
)

// GlobalClient is the process-wide xatu client. It is nil when xatu is not
// configured; all xatu-backed features must treat that as disabled.
var GlobalClient *Client

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

// NewClient connects to the configured ClickHouse endpoints. defaultNetwork is
// used as the meta_network_name filter when the config does not set one.
func NewClient(cfg *types.XatuConfig, defaultNetwork string, logger logrus.FieldLogger) (*Client, error) {
	if cfg.Raw.ClickhouseDsn == "" {
		return nil, fmt.Errorf("xatu clickhouse dsn is required")
	}

	conn, err := connect(cfg.Raw.ClickhouseDsn, cfg.Raw.Database)
	if err != nil {
		return nil, fmt.Errorf("xatu clickhouse: %w", err)
	}

	client := &Client{
		logger:      logger.WithField("module", "xatu"),
		conn:        conn,
		network:     cfg.NetworkName,
		settleDelay: cfg.SettleDelay,
	}

	if cfg.Raw.ClickhouseCachedDsn != "" {
		cachedConn, err := connect(cfg.Raw.ClickhouseCachedDsn, cfg.Raw.Database)
		if err != nil {
			return nil, fmt.Errorf("xatu cached clickhouse: %w", err)
		}

		client.cachedConn = cachedConn
	}

	if client.network == "" {
		client.network = defaultNetwork
	}

	if client.settleDelay <= 0 {
		client.settleDelay = defaultSettleDelay
	}

	concurrency := cfg.ConcurrencyLimit
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
func (c *Client) Query(ctx context.Context, settled bool, query xch.SQLQuery) (driver.Rows, error) {
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

	return conn.Query(ctx, query.Query, query.Args...)
}

// QueryPaged runs a query across every result page, calling scan for each row.
//
// The generated builders cap page_size at maxQueryPageSize and cannot express
// aggregates, so a caller reducing a whole epoch has to pull the rows and
// follow the page tokens. Reading only the first page loses rows silently,
// which is worse than being slow. build receives the token for the page to
// fetch, empty for the first.
func (c *Client) QueryPaged(
	ctx context.Context,
	settled bool,
	build func(pageToken string) (xch.SQLQuery, error),
	scan func(rows driver.Rows) error,
) error {
	offset := uint32(0)

	for page := 0; page < maxQueryPages; page++ {
		pageToken := ""
		if offset > 0 {
			pageToken = xch.EncodePageToken(offset)
		}

		query, err := build(pageToken)
		if err != nil {
			return err
		}

		rows, err := c.Query(ctx, settled, query)
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
