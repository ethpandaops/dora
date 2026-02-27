package txindexer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"

	"github.com/ethpandaops/dora/clients/execution"
	exerpc "github.com/ethpandaops/dora/clients/execution/rpc"
	"github.com/ethpandaops/dora/dbtypes"
)

// ERC20/ERC721 function selectors
var (
	selectorName     = common.Hex2Bytes("06fdde03") // name()
	selectorSymbol   = common.Hex2Bytes("95d89b41") // symbol()
	selectorDecimals = common.Hex2Bytes("313ce567") // decimals()
	selectorURI      = common.Hex2Bytes("0e89341c") // uri(uint256)
)

// erc1155Metadata represents the JSON metadata returned by ERC1155 URI.
type erc1155Metadata struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Image       string `json:"image"`
	Decimals    int    `json:"decimals"`
}

// fetchTokenMetadata fetches the name, symbol, and decimals for a token contract.
// For ERC20/ERC721: fetches on-chain via name(), symbol(), decimals() calls.
// For ERC1155: fetches via uri(0) and parses JSON metadata.
// Sets TokenFlagMetadataLoaded flag when metadata is successfully fetched.
func (t *TxIndexer) fetchTokenMetadata(ctx context.Context, token *dbtypes.ElToken) {
	contractAddr := common.BytesToAddress(token.Contract)

	// Get a client for fetching
	clients := t.indexerCtx.GetFinalizedClients(execution.AnyClient)
	if len(clients) == 0 {
		t.logger.Debug("no clients available for token metadata fetch")
		return
	}

	client := clients[0]
	rpcClient := client.GetRPCClient()
	ethClient := rpcClient.GetEthClient()
	if ethClient == nil {
		return
	}

	// Fetch metadata based on token type
	if token.TokenType == dbtypes.TokenTypeERC1155 {
		t.fetchERC1155Metadata(ctx, rpcClient, contractAddr, token)
	} else {
		t.fetchOnChainMetadata(ctx, rpcClient, contractAddr, token)
	}

	// Mark as synced and set metadata loaded flag
	token.NameSynced = uint64(time.Now().Unix())
	token.Flags |= dbtypes.TokenFlagMetadataLoaded
}

// fetchOnChainMetadata fetches ERC20/ERC721 metadata via on-chain calls.
func (t *TxIndexer) fetchOnChainMetadata(
	ctx context.Context,
	rpcClient *exerpc.ExecutionClient,
	contractAddr common.Address,
	token *dbtypes.ElToken,
) {
	// Try to fetch name
	name := t.callTokenMethod(ctx, rpcClient, contractAddr, selectorName)
	if name != "" {
		token.Name = name
	}

	// Try to fetch symbol
	symbol := t.callTokenMethod(ctx, rpcClient, contractAddr, selectorSymbol)
	if symbol != "" {
		token.Symbol = symbol
	}

	// Try to fetch decimals
	decimals := t.callTokenDecimals(ctx, rpcClient, contractAddr)
	if decimals > 0 {
		token.Decimals = decimals
	}
}

// fetchERC1155Metadata fetches ERC1155 metadata via URI and parses JSON.
func (t *TxIndexer) fetchERC1155Metadata(
	ctx context.Context,
	rpcClient *exerpc.ExecutionClient,
	contractAddr common.Address,
	token *dbtypes.ElToken,
) {
	ethClient := rpcClient.GetEthClient()
	if ethClient == nil {
		return
	}

	// Call uri(0) - ERC1155 uses token ID 0 for collection-level metadata
	// Selector: 0e89341c + uint256(0)
	data := make([]byte, 36)
	copy(data[:4], selectorURI)
	// Remaining 32 bytes are already zero (token ID = 0)

	msg := ethereum.CallMsg{
		To:   &contractAddr,
		Data: data,
	}

	result, err := ethClient.CallContract(ctx, msg, nil)
	if err != nil {
		t.logger.WithField("contract", contractAddr.Hex()).Debug("failed to call uri(0)")
		return
	}

	uri := decodeString(result)
	if uri == "" {
		return
	}

	// Replace {id} placeholder with 0 (64-char hex, no 0x prefix)
	uri = strings.ReplaceAll(uri, "{id}", fmt.Sprintf("%064x", 0))

	// Store the URI
	token.MetadataURI = uri

	// Fetch and parse JSON metadata
	metadata := t.fetchMetadataFromURI(ctx, uri)
	if metadata == nil {
		return
	}

	// Apply metadata to token
	if metadata.Name != "" {
		token.Name = metadata.Name
		// ERC1155 doesn't have symbol, use name as symbol too
		token.Symbol = metadata.Name
	}
	if metadata.Decimals >= 0 && metadata.Decimals <= 255 {
		token.Decimals = uint8(metadata.Decimals)
	}
}

// fetchMetadataFromURI fetches JSON metadata from a URI.
// Handles IPFS URIs by converting to public gateway.
func (t *TxIndexer) fetchMetadataFromURI(ctx context.Context, uri string) *erc1155Metadata {
	// Convert IPFS URIs to public gateway
	if strings.HasPrefix(uri, "ipfs://") {
		uri = "https://ipfs.io/ipfs/" + strings.TrimPrefix(uri, "ipfs://")
	}

	// Only fetch HTTP(S) URLs
	if !strings.HasPrefix(uri, "http://") && !strings.HasPrefix(uri, "https://") {
		return nil
	}

	// Create HTTP client with 10 second timeout
	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return nil
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		t.logger.WithField("uri", uri).Debug("failed to fetch metadata URI")
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	// Limit response size to 1MB
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil
	}

	var metadata erc1155Metadata
	if err := json.Unmarshal(body, &metadata); err != nil {
		return nil
	}

	return &metadata
}

// callTokenMethod calls a string-returning method on a token contract.
func (t *TxIndexer) callTokenMethod(
	ctx context.Context,
	rpcClient *exerpc.ExecutionClient,
	contractAddr common.Address,
	selector []byte,
) string {
	ethClient := rpcClient.GetEthClient()
	if ethClient == nil {
		return ""
	}

	msg := ethereum.CallMsg{
		To:   &contractAddr,
		Data: selector,
	}

	result, err := ethClient.CallContract(ctx, msg, nil)
	if err != nil {
		return ""
	}

	return decodeString(result)
}

// callTokenDecimals calls the decimals() method on a token contract.
func (t *TxIndexer) callTokenDecimals(
	ctx context.Context,
	rpcClient *exerpc.ExecutionClient,
	contractAddr common.Address,
) uint8 {
	ethClient := rpcClient.GetEthClient()
	if ethClient == nil {
		return 0
	}

	msg := ethereum.CallMsg{
		To:   &contractAddr,
		Data: selectorDecimals,
	}

	result, err := ethClient.CallContract(ctx, msg, nil)
	if err != nil {
		return 0
	}

	if len(result) < 32 {
		return 0
	}

	// Decimals is returned as uint8 (last byte of uint256)
	decimals := new(big.Int).SetBytes(result).Uint64()
	if decimals > 255 {
		return 18 // Fallback to 18 if invalid
	}
	return uint8(decimals)
}

// decodeString decodes an ABI-encoded string from contract call result.
// Handles both:
// - Standard ABI encoding: offset (32) + length (32) + data
// - Direct string bytes (non-standard but used by some contracts)
func decodeString(data []byte) string {
	if len(data) == 0 {
		return ""
	}

	// Try standard ABI decoding first
	if len(data) >= 64 {
		// Read offset (should be 32 for a single string)
		offset := new(big.Int).SetBytes(data[:32]).Uint64()

		if offset == 32 && len(data) >= 64 {
			// Read length
			length := new(big.Int).SetBytes(data[32:64]).Uint64()

			// Validate length
			if length > 0 && len(data) >= int(64+length) {
				str := string(data[64 : 64+length])
				return sanitizeString(str)
			}
		}
	}

	// Try bytes32 format (some tokens return fixed-size strings)
	if len(data) == 32 {
		// Find null terminator or end
		end := 0
		for i := 0; i < 32; i++ {
			if data[i] == 0 {
				break
			}
			end = i + 1
		}
		if end > 0 {
			return sanitizeString(string(data[:end]))
		}
	}

	return ""
}

// sanitizeString removes non-printable characters and trims whitespace.
func sanitizeString(s string) string {
	// Remove null bytes and other control characters
	result := strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s)

	return strings.TrimSpace(result)
}
