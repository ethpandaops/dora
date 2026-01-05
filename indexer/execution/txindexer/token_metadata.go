package txindexer

import (
	"context"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"

	"github.com/ethpandaops/dora/clients/execution"
	exerpc "github.com/ethpandaops/dora/clients/execution/rpc"
	"github.com/ethpandaops/dora/dbtypes"
)

// ERC20 function selectors
var (
	selectorName     = common.Hex2Bytes("06fdde03") // name()
	selectorSymbol   = common.Hex2Bytes("95d89b41") // symbol()
	selectorDecimals = common.Hex2Bytes("313ce567") // decimals()
)

// fetchTokenMetadata fetches the name, symbol, and decimals for a token contract.
// Returns updated token with NameSynced set to current timestamp.
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

	// Mark as synced
	token.NameSynced = uint64(time.Now().Unix())
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
