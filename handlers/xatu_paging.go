package handlers

import (
	cbtch "github.com/ethpandaops/xatu-cbt/pkg/proto/clickhouse"
	xch "github.com/ethpandaops/xatu/pkg/proto/clickhouse"
)

// xatuPageToken encodes a QueryPaged row offset into a page token for the
// xatu raw table builders. The zero offset must stay an empty token: the
// builders treat "" as page one, and a token of zero would be decoded and
// re-applied as OFFSET 0 anyway.
func xatuPageToken(pageOffset uint32) string {
	if pageOffset == 0 {
		return ""
	}

	return xch.EncodePageToken(pageOffset)
}

// cbtPageToken is xatuPageToken for the xatu-cbt table builders. The two
// generated packages use the same token format, but each builder only decodes
// tokens produced by its own package's helper.
func cbtPageToken(pageOffset uint32) string {
	if pageOffset == 0 {
		return ""
	}

	return cbtch.EncodePageToken(pageOffset)
}
