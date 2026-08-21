import React from 'react';
import { useAccount, useBalance, useConfig, useSendTransaction } from 'wagmi';
import { createWalletClient, http } from 'viem';
import type { HDAccount } from 'viem';

import { IValidator } from '../SubmitConsolidationsForm/SubmitConsolidationsFormProps';
import TopupDepositForm from '../SubmitDepositsForm/TopupDepositForm';
import { useQueueDataCache } from '../../hooks/useQueueDataCache';
import { computeBuilderFees } from '../SubmitShared/builderDepositFee';
import { toReadableAmount } from '../../utils/ReadableAmount';

const GWEI = 1000000000n;

interface ITopupBuilderFormProps {
  builderDepositContract: string;
  loadBuilders?: (address: string) => Promise<IValidator[]>;
  searchBuilders?: (searchTerm: string) => Promise<IValidator[]>;
  explorerUrl?: string;
  localAccount?: HDAccount;
}

// Builder top-up: a builder deposit with an existing builder's pubkey acts as a
// top-up at onboarding (the proof-of-possession is only checked for new builders,
// so the signature is zeroed - same as validator top-ups). Reuses the validator
// topup form; only the fee handling and raw calldata differ. Builders have no max
// effective balance, so the top-up is capped by the funding wallet's balance.
const TopupBuilderForm = (props: ITopupBuilderFormProps): React.ReactElement => {
  const { address, chain: connectedChain } = useAccount();
  const wagmiConfig = useConfig();
  const chain = connectedChain ?? wagmiConfig.chains[0];
  const submitRequest = useSendTransaction();

  const { queueData, logData: cachedLogData } = useQueueDataCache(props.builderDepositContract, chain?.id);
  const fees = computeBuilderFees(queueData, cachedLogData, true);

  // no max effective balance for builders: cap by the funding wallet's balance
  const fundingAddress = (props.localAccount?.address ?? address) as `0x${string}` | undefined;
  const fundingBalance = useBalance({
    address: fundingAddress,
    query: { refetchInterval: 12000 },
  });
  const maxTopupEth = fundingBalance.data !== undefined
    ? Math.max(0, Math.floor((Number(fundingBalance.data.value) / 1e18 - 0.1) * 10000) / 10000) // keep ~0.1 ETH for gas & queue fee
    : 0;

  const submitTopup = (pubkey: string, amountGwei: bigint): Promise<string> => {
    // calldata (184 bytes): pubkey(48) ++ withdrawal_credentials(32, ignored for
    // top-ups) ++ amount(8, big-endian) ++ signature(96, zeroed - not verified for top-ups)
    const pubkeyHex = pubkey.replace(/^0x/, "");
    const creds = "b0" + "00".repeat(31); // 0xb0 prefix + 31 zero bytes = 32 bytes
    const amountHex = amountGwei.toString(16).padStart(16, "0");
    const signature = "00".repeat(96);
    const data = ("0x" + pubkeyHex + creds + amountHex + signature) as `0x${string}`;
    const value = amountGwei * GWEI + fees.requestFee;

    if (props.localAccount) {
      const walletClient = createWalletClient({
        account: props.localAccount,
        chain,
        transport: http(),
      });
      return walletClient.sendTransaction({
        to: props.builderDepositContract as `0x${string}`,
        value,
        data,
      });
    }
    return submitRequest.sendTransactionAsync({
      to: props.builderDepositContract as `0x${string}`,
      account: address,
      chainId: chain?.id,
      value,
      data,
    });
  };

  if (fees.isPreFork) {
    return (
      <div className="alert alert-danger mt-3">
        The network is not on Gloas yet, so builder deposit requests can not be submitted.
      </div>
    );
  }

  return (
    <TopupDepositForm
      loadValidators={props.loadBuilders}
      searchValidators={props.searchBuilders}
      depositContract={props.builderDepositContract}
      explorerUrl={props.explorerUrl}
      localAccount={props.localAccount}
      entityName="builder"
      entityLinkBase="/builder/"
      customSubmit={submitTopup}
      maxTopupEthOverride={maxTopupEth}
      extraFeeNote={<span>{toReadableAmount(fees.requestFee, 0, 'Wei', 0)} (incl. extra fee cushion)</span>}
    />
  );
};

export default TopupBuilderForm;
