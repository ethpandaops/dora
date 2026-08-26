import React from 'react';
import { useBalance } from 'wagmi';

import FaucetButton from '../SubmitDepositsForm/FaucetButton';
import { toReadableAmount } from '../../utils/ReadableAmount';

interface ILocalWalletBannerProps {
  address: `0x${string}`;
  faucetEnabled?: boolean;
  faucetAmount?: number;
  explorerUrl?: string;
}

// Info banner for wallet-free submission: shows the generated wallet, its live
// balance, and a faucet request button. Used by both deposit submit pages.
const LocalWalletBanner = (props: ILocalWalletBannerProps): React.ReactElement => {
  const balance = useBalance({
    address: props.address,
    query: { refetchInterval: 12000 },
  });

  return (
    <div className="alert alert-info mt-2">
      <i className="fa fa-wallet me-2"></i>
      No wallet connected - deposits will be submitted from the generated wallet{' '}
      <span className="font-monospace">{props.address}</span>
      {balance.data !== undefined && (
        <> (balance: {toReadableAmount(balance.data.value, 18, 'ETH', 4)})</>
      )}
      {props.faucetEnabled && (
        <span className="ms-2">
          <FaucetButton
            address={props.address}
            amount={props.faucetAmount || 50}
            explorerUrl={props.explorerUrl}
          />
        </span>
      )}
    </div>
  );
};

export default LocalWalletBanner;
