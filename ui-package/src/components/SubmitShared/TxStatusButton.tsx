import React from 'react';

import { TrackedTx } from './useTrackedTx';
import { shortTxHash, txExplorerLink } from './txLink';

interface ITxStatusButtonProps {
  tx: TrackedTx;
  onSubmit(): void;
  disabled?: boolean;
  idleLabel: React.ReactNode;
  confirmedLabel?: string;
  small?: boolean;
  explorerUrl?: string;
}

// Submit button with full tx lifecycle display: Submit -> Signing -> Pending
// (with tx link) -> Submitted badge / Failed-on-chain badge with Retry.
const TxStatusButton = (props: ITxStatusButtonProps): React.ReactElement => {
  const { tx } = props;
  const btnClass = props.small ? 'btn btn-sm' : 'btn';

  const link = tx.hash ? (
    <a href={txExplorerLink(props.explorerUrl, tx.hash)} target="_blank" rel="noreferrer" className="font-monospace" title={tx.hash}>
      {shortTxHash(tx.hash)}
    </a>
  ) : null;

  if (tx.status === 'confirmed') {
    return (
      <div className="d-flex flex-column align-items-start">
        <span className="badge rounded-pill text-bg-success">
          <i className="fa fa-check me-1"></i>
          {props.confirmedLabel ?? 'Submitted'}
        </span>
        <span className="small mt-1">{link}</span>
      </div>
    );
  }

  if (tx.status === 'reverted') {
    return (
      <div className="d-flex flex-column align-items-start">
        <span className="badge rounded-pill text-bg-danger" title="The transaction was included in a block but reverted on-chain">
          <i className="fa fa-times me-1"></i>
          Failed on-chain
        </span>
        <span className="small mt-1">{link}</span>
        <button className={`${btnClass} btn-outline-danger mt-1 text-nowrap`} onClick={() => props.onSubmit()}>
          <i className="fa-solid fa-repeat me-1"></i>
          Retry
        </button>
      </div>
    );
  }

  if (tx.status === 'pending') {
    return (
      <div className="d-flex flex-column align-items-start">
        <button className={`${btnClass} btn-outline-warning text-nowrap`} disabled>
          <span className="spinner-border spinner-border-sm me-1"></span>
          Pending
        </button>
        <span className="small mt-1">{link}</span>
      </div>
    );
  }

  return (
    <button
      className={`${btnClass} btn-primary text-nowrap`}
      disabled={props.disabled || tx.status === 'submitting'}
      onClick={() => props.onSubmit()}
    >
      {tx.status === 'submitting' ? (
        <span><span className="spinner-border spinner-border-sm me-1"></span>Signing...</span>
      ) : tx.status === 'error' ? (
        <span><i className="fa-solid fa-repeat me-1"></i>Retry</span>
      ) : (
        props.idleLabel
      )}
    </button>
  );
};

export default TxStatusButton;
