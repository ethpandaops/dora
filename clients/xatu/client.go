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
	defaultDatabase         = "default"
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
	database    string
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
		database:    cfg.Raw.Database,
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

	if client.database == "" {
		client.database = defaultDatabase
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

// Database returns the ClickHouse database queries run against.
func (c *Client) Database() string {
	return c.database
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
