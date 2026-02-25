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

// newAccountUpdate holds info for creating a new account with balance.
type newAccountUpdate struct {
	address []byte
	balance *big.Int
}

// ProcessPendingLookups processes pending balance lookups and returns updated balances.
// For known accounts (AccountID > 0), updates the balance directly.
// For unknown accounts (AccountID == 0), creates the account if balance > 0.
func (s *BalanceLookupService) ProcessPendingLookups(ctx context.Context) (dbCommitCallback, error) {
	requests := s.GetPendingLookups(maxBalanceLookupsPerBlock)
	if len(requests) == 0 {
		return nil, nil
	}

	s.logger.WithField("count", len(requests)).Debug("processing balance lookups")

	knownUpdates := make([]*dbtypes.ElBalance, 0, len(requests))
	newAccounts := make([]*newAccountUpdate, 0)
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

		if req.AccountID > 0 {
			// Known account - update balance directly
			knownUpdates = append(knownUpdates, &dbtypes.ElBalance{
				AccountID:  req.AccountID,
				TokenID:    req.TokenID,
				Balance:    weiToFloat(balance, req.Decimals),
				BalanceRaw: balance.Bytes(),
				Updated:    currentTime,
			})
		} else if balance.Sign() > 0 {
			// Unknown account with positive balance - need to create account
			newAccounts = append(newAccounts, &newAccountUpdate{
				address: req.Address.Bytes(),
				balance: balance,
			})
		}
		// Unknown accounts with zero balance are ignored
	}

	// If nothing to update, return nil
	if len(knownUpdates) == 0 && len(newAccounts) == 0 {
		return nil, nil
	}

	return func(commitCtx context.Context, tx *sqlx.Tx) error {
		// Insert known account balance updates
		if len(knownUpdates) > 0 {
			if err := db.InsertElBalances(commitCtx, tx, knownUpdates); err != nil {
				return fmt.Errorf("failed to insert balance updates: %w", err)
			}
		}

		// Create new accounts and their balances
		for _, acc := range newAccounts {
			newAccount := &dbtypes.ElAccount{
				Address: acc.address,
			}

			accountID, err := db.InsertElAccount(commitCtx, tx, newAccount)
			if err != nil {
				s.logger.WithError(err).WithField("address", common.BytesToAddress(acc.address).Hex()).
					Warn("failed to create account for unknown address with balance")
				continue
			}

			balanceRecord := &dbtypes.ElBalance{
				AccountID:  accountID,
				TokenID:    nativeTokenID,
				Balance:    weiToFloat(acc.balance, 18),
				BalanceRaw: acc.balance.Bytes(),
				Updated:    currentTime,
			}

			if err := db.InsertElBalances(commitCtx, tx, []*dbtypes.ElBalance{balanceRecord}); err != nil {
				s.logger.WithError(err).WithField("address", common.BytesToAddress(acc.address).Hex()).
					Warn("failed to insert balance for new account")
				continue
			}

			s.logger.WithFields(logrus.Fields{
				"address":   common.BytesToAddress(acc.address).Hex(),
				"accountID": accountID,
				"balance":   weiToFloat(acc.balance, 18),
			}).Debug("created account and balance for unknown address")
		}

		return nil
	}, nil
}
