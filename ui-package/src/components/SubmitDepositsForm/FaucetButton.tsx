import React, { useState } from 'react';
import { useWaitForTransactionReceipt } from 'wagmi';

interface IFaucetButtonProps {
  address?: string;
  amount: number; // ETH per request
  explorerUrl?: string;
}

// FaucetButton requests devnet funds from dora's faucet endpoint for the given address
// and tracks the funding transaction until it is confirmed on-chain.
const FaucetButton = (props: IFaucetButtonProps): React.ReactElement => {
  const [isRequesting, setIsRequesting] = useState(false);
  const [txHash, setTxHash] = useState<`0x${string}` | null>(null);
  const [error, setError] = useState<string | null>(null);

  const receipt = useWaitForTransactionReceipt({
    hash: txHash ?? undefined,
    query: { enabled: !!txHash },
  });

  const isPending = !!txHash && receipt.isLoading;
  const isConfirmed = receipt.data?.status === 'success';
  const isFailed = receipt.data?.status === 'reverted';

  const requestFunds = async () => {
    if (!props.address || isRequesting || isPending) return;

    setIsRequesting(true);
    setError(null);
    setTxHash(null);

    try {
      const response = await fetch('/validators/deposits/faucet', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ address: props.address }),
      });
      const result = await response.json();
      if (result.status === 'ok') {
        setTxHash(result.txhash as `0x${string}`);
      } else {
        setError(result.message || 'Faucet request failed');
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setIsRequesting(false);
    }
  };

  const txLink = (label: string) => {
    if (!txHash) return null;
    const shortHash = `${txHash.substring(0, 10)}…${txHash.substring(txHash.length - 8)}`;
    // fall back to dora's own tx page when no external explorer is configured
    const url = props.explorerUrl ? `${props.explorerUrl.replace(/\/$/, '')}/tx/${txHash}` : `/tx/${txHash}`;
    return (
      <a href={url} target="_blank" rel="noreferrer" className="font-monospace" title={txHash}>
        {label || shortHash}
      </a>
    );
  };

  return (
    <span className="d-inline-flex align-items-center gap-2 flex-wrap">
      <button
        type="button"
        className="btn btn-sm btn-outline-success text-nowrap"
        onClick={requestFunds}
        disabled={!props.address || isRequesting || isPending}
        title={`Send ${props.amount} ETH devnet funds to ${props.address || 'this address'}`}
      >
        {isRequesting || isPending ? (
          <span className="spinner-border spinner-border-sm me-1"></span>
        ) : (
          <i className="fa fa-tint me-1"></i>
        )}
        Request {props.amount} ETH
      </button>
      {isPending && (
        <span className="text-warning" style={{ fontSize: '0.85em' }}>
          <i className="fa fa-hourglass-half me-1"></i>
          {props.amount} ETH incoming - waiting for confirmation ({txLink('tx')})
        </span>
      )}
      {isConfirmed && (
        <span className="text-success" style={{ fontSize: '0.85em' }}>
          <i className="fa fa-check me-1"></i>
          {props.amount} ETH received ({txLink('tx')})
        </span>
      )}
      {isFailed && (
        <span className="text-danger" style={{ fontSize: '0.85em' }}>
          <i className="fa fa-times-circle me-1"></i>
          Funding transaction failed on-chain ({txLink('tx')}) - try again
        </span>
      )}
      {error && (
        <span className="text-danger" style={{ fontSize: '0.85em' }}>
          <i className="fa fa-times-circle me-1"></i>
          {error}
        </span>
      )}
    </span>
  );
};

export default FaucetButton;
