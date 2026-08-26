import React, { useState } from 'react';
import { useAccount, useConfig, useSendTransaction, useWaitForTransactionReceipt } from 'wagmi';
import { createWalletClient, http, encodeDeployData } from 'viem';
import type { HDAccount } from 'viem';

import { BATCHER_ABI, BATCHER_BYTECODE } from './DepositBatcher';
import { shortTxHash, txExplorerLink } from './txLink';

interface IBatchSubmitButtonProps {
  // contract receiving the batched calls
  target: string;
  // per-item calldata and msg.value; called at click time so fees are current
  buildBatch: () => { calls: `0x${string}`[]; values: bigint[] };
  count: number;
  localAccount?: HDAccount;
  explorerUrl?: string;
}

interface IBatchTxState {
  pending?: boolean;
  hash?: string;
  error?: string;
}

// "Submit All" for the deposit submit pages: one transaction that deploys a
// throwaway batching contract whose constructor performs every call and
// self-destructs, refunding leftover value to the sender.
const BatchSubmitButton = (props: IBatchSubmitButtonProps): React.ReactElement | null => {
  const { address, chain: connectedChain, isConnected } = useAccount();
  const wagmiConfig = useConfig();
  const chain = connectedChain ?? wagmiConfig.chains[0];
  const batchRequest = useSendTransaction();
  const [batchTx, setBatchTx] = useState<IBatchTxState>({});

  // track the batch tx until it is actually mined
  const receipt = useWaitForTransactionReceipt({
    hash: batchTx.hash as `0x${string}` | undefined,
    query: { enabled: !!batchTx.hash },
  });
  const isWaiting = !!batchTx.hash && receipt.isLoading;
  const isConfirmed = receipt.data?.status === 'success';
  const isReverted = receipt.data?.status === 'reverted';

  if (!props.localAccount && !isConnected) {
    return null;
  }

  const submitAll = async () => {
    if (batchTx.pending || isWaiting || isConfirmed || props.count === 0) return;

    setBatchTx({ pending: true });

    try {
      const { calls, values } = props.buildBatch();
      const totalValue = values.reduce((sum, v) => sum + v, 0n);

      const deployData = encodeDeployData({
        abi: BATCHER_ABI,
        bytecode: BATCHER_BYTECODE,
        args: [props.target as `0x${string}`, values, calls],
      });

      let hash: string;
      if (props.localAccount) {
        const walletClient = createWalletClient({
          account: props.localAccount,
          chain,
          transport: http(),
        });
        hash = await walletClient.sendTransaction({
          data: deployData,
          value: totalValue,
        });
      } else {
        hash = await batchRequest.sendTransactionAsync({
          data: deployData,
          value: totalValue,
          account: address,
          chainId: chain?.id,
        });
      }

      setBatchTx({ hash });
    } catch (error) {
      setBatchTx({ error: error instanceof Error ? error.message : String(error) });
    }
  };

  return (
    <div className="row mt-2">
      <div className="col-12 d-flex align-items-center gap-2 flex-wrap">
        <button
          className="btn btn-sm btn-primary text-nowrap"
          disabled={batchTx.pending || isWaiting || isConfirmed}
          onClick={() => submitAll()}
          title="One transaction: deploys a throwaway batching contract whose constructor submits all deposits and refunds leftover value"
        >
          {isConfirmed ? (
            <span><i className="fa fa-check me-1"></i>All Sent</span>
          ) : batchTx.pending || isWaiting ? (
            <span><span className="spinner-border spinner-border-sm me-1"></span>{isWaiting ? 'Pending...' : 'Submitting batch...'}</span>
          ) : isReverted ? (
            <span><i className="fa-solid fa-repeat me-1"></i>Retry</span>
          ) : (
            <span><i className="fa fa-layer-group me-1"></i>Submit All ({props.count} deposits, 1 tx)</span>
          )}
        </button>
        {isWaiting && batchTx.hash && (
          <span className="text-warning small">
            <i className="fa fa-hourglass-half me-1"></i>
            waiting for confirmation (
            <a href={txExplorerLink(props.explorerUrl, batchTx.hash)} target="_blank" rel="noreferrer" className="font-monospace" title={batchTx.hash}>tx</a>
            )
          </span>
        )}
        {isConfirmed && batchTx.hash && (
          <a href={txExplorerLink(props.explorerUrl, batchTx.hash)} target="_blank" rel="noreferrer" className="font-monospace small" title={batchTx.hash}>
            {shortTxHash(batchTx.hash)}
          </a>
        )}
        {isReverted && batchTx.hash && (
          <span className="text-danger small">
            <i className="fa fa-times-circle me-1"></i>
            batch transaction failed on-chain (
            <a href={txExplorerLink(props.explorerUrl, batchTx.hash)} target="_blank" rel="noreferrer" className="font-monospace" title={batchTx.hash}>tx</a>
            )
          </span>
        )}
        {batchTx.error && (
          <span className="text-danger small">
            <i className="fa fa-times-circle me-1"></i>
            {batchTx.error}
          </span>
        )}
      </div>
    </div>
  );
};

export default BatchSubmitButton;
