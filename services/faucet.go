package services

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/clients/execution"
	"github.com/ethpandaops/dora/utils"
)

const (
	faucetDefaultAmountEth = 50
	faucetDefaultCooldown  = 30 * time.Second
	faucetTransferGasLimit = 21000
)

// FaucetService sends funds from prefunded wallets to user supplied addresses.
// It is a devnet helper for the submit deposit pages and is only active when
// frontend.faucet is configured with at least one private key.
type FaucetService struct {
	logger  logrus.FieldLogger
	wallets []*faucetWallet

	sendMutex    sync.Mutex
	cooldownTime map[common.Address]time.Time
}

type faucetWallet struct {
	privkey *ecdsa.PrivateKey
	address common.Address
	nonce   uint64
}

var GlobalFaucetService *FaucetService
var faucetServiceInit sync.Once

// GetFaucetService returns the global faucet service, initializing it on first use.
func GetFaucetService() *FaucetService {
	faucetServiceInit.Do(func() {
		service := &FaucetService{
			logger:       logrus.StandardLogger().WithField("module", "faucet"),
			cooldownTime: map[common.Address]time.Time{},
		}

		if utils.Config.Frontend.Faucet.Enabled {
			for idx, keyHex := range utils.Config.Frontend.Faucet.PrivateKeys {
				privkey, err := crypto.HexToECDSA(cleanHexPrefix(keyHex))
				if err != nil {
					service.logger.Errorf("could not parse faucet private key %v: %v", idx, err)
					continue
				}
				service.wallets = append(service.wallets, &faucetWallet{
					privkey: privkey,
					address: crypto.PubkeyToAddress(privkey.PublicKey),
				})
			}
		}

		GlobalFaucetService = service
	})

	return GlobalFaucetService
}

func cleanHexPrefix(hex string) string {
	if len(hex) >= 2 && hex[0:2] == "0x" {
		return hex[2:]
	}
	return hex
}

// IsEnabled returns true if the faucet is configured and has usable funding wallets.
func (fs *FaucetService) IsEnabled() bool {
	return utils.Config.Frontend.Faucet.Enabled && len(fs.wallets) > 0
}

// GetFundingAmountEth returns the amount of ETH sent per faucet request.
func (fs *FaucetService) GetFundingAmountEth() float64 {
	amount := utils.Config.Frontend.Faucet.FundingAmount
	if amount <= 0 {
		amount = faucetDefaultAmountEth
	}
	return amount
}

func (fs *FaucetService) getCooldown() time.Duration {
	cooldown := utils.Config.Frontend.Faucet.Cooldown
	if cooldown <= 0 {
		cooldown = faucetDefaultCooldown
	}
	return cooldown
}

func (fs *FaucetService) getFundingAmountWei() *big.Int {
	amountWei, _ := new(big.Float).Mul(
		big.NewFloat(fs.GetFundingAmountEth()),
		big.NewFloat(1e18),
	).Int(nil)
	return amountWei
}

// RequestFunds sends the configured funding amount to the given address from the
// first funding wallet with a sufficient balance. Requests are serialized to keep
// nonce tracking simple; a per-recipient cooldown protects against accidental spam.
func (fs *FaucetService) RequestFunds(ctx context.Context, to common.Address) (common.Hash, error) {
	if !fs.IsEnabled() {
		return common.Hash{}, fmt.Errorf("faucet is not enabled")
	}

	fs.sendMutex.Lock()
	defer fs.sendMutex.Unlock()

	if lastRequest, found := fs.cooldownTime[to]; found {
		if wait := time.Until(lastRequest.Add(fs.getCooldown())); wait > 0 {
			return common.Hash{}, fmt.Errorf("please wait %v before requesting funds for this address again", wait.Round(time.Second))
		}
	}

	client := fs.getReadyClient()
	if client == nil {
		return common.Hash{}, fmt.Errorf("no ready execution client available")
	}

	rpcClient := client.GetRPCClient()
	ethClient := rpcClient.GetEthClient()

	chainID, err := ethClient.ChainID(ctx)
	if err != nil {
		return common.Hash{}, fmt.Errorf("could not get chain id: %w", err)
	}

	amountWei := fs.getFundingAmountWei()

	// select the first funding wallet that can cover the transfer + fees
	head, err := ethClient.HeaderByNumber(ctx, nil)
	if err != nil {
		return common.Hash{}, fmt.Errorf("could not get chain head: %w", err)
	}

	gasTipCap, err := ethClient.SuggestGasTipCap(ctx)
	if err != nil || gasTipCap.Sign() == 0 {
		gasTipCap = big.NewInt(1e9) // 1 gwei fallback
	}
	gasFeeCap := new(big.Int).Add(gasTipCap, big.NewInt(1e9))
	if head.BaseFee != nil {
		gasFeeCap = gasFeeCap.Add(gasFeeCap, new(big.Int).Mul(head.BaseFee, big.NewInt(2)))
	}

	// Estimate the gas for the transfer instead of assuming the classic 21000:
	// gas-repriced devnets (glamsterdam) charge far more for cold/new account access.
	gasLimit := uint64(faucetTransferGasLimit)
	if estimated, err := ethClient.EstimateGas(ctx, ethereum.CallMsg{
		From:  fs.wallets[0].address,
		To:    &to,
		Value: amountWei,
	}); err == nil && estimated > gasLimit {
		gasLimit = estimated
	}
	gasLimit = gasLimit * 5 / 4 // 25% headroom

	maxFeeWei := new(big.Int).Mul(gasFeeCap, new(big.Int).SetUint64(gasLimit))
	requiredWei := new(big.Int).Add(amountWei, maxFeeWei)

	var wallet *faucetWallet
	for _, candidate := range fs.wallets {
		balance, err := ethClient.BalanceAt(ctx, candidate.address, nil)
		if err != nil {
			fs.logger.Warnf("could not get balance for faucet wallet %v: %v", candidate.address, err)
			continue
		}
		if balance.Cmp(requiredWei) >= 0 {
			wallet = candidate
			break
		}
	}
	if wallet == nil {
		return common.Hash{}, fmt.Errorf("no faucet wallet with sufficient balance")
	}

	nonce, err := ethClient.PendingNonceAt(ctx, wallet.address)
	if err != nil {
		return common.Hash{}, fmt.Errorf("could not get nonce: %w", err)
	}
	if wallet.nonce > nonce {
		// keep our own counter ahead of the node's view for consecutive requests
		nonce = wallet.nonce
	}

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     nonce,
		GasTipCap: gasTipCap,
		GasFeeCap: gasFeeCap,
		Gas:       gasLimit,
		To:        &to,
		Value:     amountWei,
	})

	signedTx, err := types.SignTx(tx, types.LatestSignerForChainID(chainID), wallet.privkey)
	if err != nil {
		return common.Hash{}, fmt.Errorf("could not sign transaction: %w", err)
	}

	err = rpcClient.SendTransaction(ctx, signedTx)
	if err != nil {
		return common.Hash{}, fmt.Errorf("could not send transaction: %w", err)
	}

	wallet.nonce = nonce + 1
	fs.cooldownTime[to] = time.Now()

	fs.logger.Infof("sent %v ETH from %v to %v (tx: %v)", fs.GetFundingAmountEth(), wallet.address, to, signedTx.Hash())

	return signedTx.Hash(), nil
}

func (fs *FaucetService) getReadyClient() *execution.Client {
	for _, client := range GlobalBeaconService.GetExecutionClients() {
		if client.GetStatus() == execution.ClientStatusOnline {
			return client
		}
	}
	return nil
}
