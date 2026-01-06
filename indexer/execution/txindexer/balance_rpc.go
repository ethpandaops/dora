package txindexer

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/clients/execution"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
)

// ERC20 balanceOf(address) function selector
var selectorBalanceOf = common.Hex2Bytes("70a08231")

// FetchETHBalance fetches the native ETH balance for an address via eth_getBalance.
func (s *BalanceLookupService) FetchETHBalance(ctx context.Context, address common.Address) (*big.Int, error) {
	clients := s.indexer.indexerCtx.GetFinalizedClients(execution.AnyClient)
	if len(clients) == 0 {
		return nil, fmt.Errorf("no clients available")
	}

	client := clients[0]
	rpcClient := client.GetRPCClient()

	balance, err := rpcClient.GetBalanceAt(ctx, address, nil)
	if err != nil {
		return nil, fmt.Errorf("eth_getBalance failed: %w", err)
	}

	return balance, nil
}

// FetchTokenBalance fetches the ERC20 token balance for an address via balanceOf static call.
func (s *BalanceLookupService) FetchTokenBalance(ctx context.Context, address common.Address, tokenContract common.Address) (*big.Int, error) {
	clients := s.indexer.indexerCtx.GetFinalizedClients(execution.AnyClient)
	if len(clients) == 0 {
		return nil, fmt.Errorf("no clients available")
	}

	client := clients[0]
	rpcClient := client.GetRPCClient()
	ethClient := rpcClient.GetEthClient()
	if ethClient == nil {
		return nil, fmt.Errorf("ethclient not available")
	}

	// Build balanceOf(address) call data
	// selector (4 bytes) + address padded to 32 bytes
	callData := make([]byte, 36)
	copy(callData[:4], selectorBalanceOf)
	copy(callData[16:], address.Bytes()) // Address is 20 bytes, left-padded with zeros

	msg := ethereum.CallMsg{
		To:   &tokenContract,
		Data: callData,
	}

	result, err := ethClient.CallContract(ctx, msg, nil)
	if err != nil {
		return nil, fmt.Errorf("balanceOf call failed: %w", err)
	}

	if len(result) < 32 {
		return big.NewInt(0), nil
	}

	return new(big.Int).SetBytes(result), nil
}

// ProcessPendingLookups processes pending balance lookups and returns updated balances.
// RPC lookups set the Updated timestamp to mark them as verified.
func (s *BalanceLookupService) ProcessPendingLookups(ctx context.Context) (dbCommitCallback, error) {
	requests := s.GetPendingLookups(maxBalanceLookupsPerBlock)
	if len(requests) == 0 {
		return nil, nil
	}

	s.logger.WithField("count", len(requests)).Debug("processing balance lookups")

	updatedBalances := make([]*dbtypes.ElBalance, 0, len(requests))
	currentTime := uint64(time.Now().Unix())

	for _, req := range requests {
		var balance *big.Int
		var err error

		if req.TokenID == nativeTokenID {
			balance, err = s.FetchETHBalance(ctx, req.Address)
		} else {
			balance, err = s.FetchTokenBalance(ctx, req.Address, req.Contract)
		}

		if err != nil {
			s.logger.WithError(err).WithFields(logrus.Fields{
				"accountID": req.AccountID,
				"tokenID":   req.TokenID,
				"address":   req.Address.Hex(),
			}).Debug("failed to fetch balance")
			continue
		}

		updatedBalances = append(updatedBalances, &dbtypes.ElBalance{
			AccountID:  req.AccountID,
			TokenID:    req.TokenID,
			Balance:    weiToFloat(balance, req.Decimals),
			BalanceRaw: balance.Bytes(),
			Updated:    currentTime, // RPC lookups set the Updated timestamp
		})
	}

	// Insert updated balances to DB
	if len(updatedBalances) == 0 {
		return nil, nil
	}

	return func(tx *sqlx.Tx) error {
		return db.InsertElBalances(updatedBalances, tx)
	}, nil
}
