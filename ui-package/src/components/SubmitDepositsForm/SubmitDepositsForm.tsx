import React from 'react';
import { ConnectButton } from '@rainbow-me/rainbowkit';

import { ISubmitDepositsFormProps } from './SubmitDepositsFormProps';
import './SubmitDepositsForm.scss';
import { useAccount } from 'wagmi';

export interface ISubmitDepositsFormState {
}

const VaultPage = (props: ISubmitDepositsFormProps): React.ReactElement => {
  const { address: walletAddress, isConnected, chain } = useAccount();

  return (
    <div className="container submit-deposits">
      <div className="row">
        <div className="col-12">
          <h1>Submit validator deposits</h1>
          <p>This tool can be used to submit validator deposits to the deposit contract.</p>
          <p>You can find instructions on how to generate deposits at the <a href="https://launchpad.ethereum.org/en/overview" target="_blank" rel="noreferrer">Staking Launchpad</a>.</p>
          <div className="alert alert-danger">
            <b>Don't provide your keystore or mnemonic to us or any other website</b>
          </div>
        </div>
      </div>

      <div className="row mt-3">
        <div className="col-12">
          <b>Step 1: Connect your wallet</b>
        </div>
      </div>
      <div className="row">
        <div className="col-12 p-2">
          <ConnectButton showBalance={true} accountStatus={{ smallScreen: 'avatar', largeScreen: 'full' }} chainStatus={{ smallScreen: 'icon', largeScreen: 'full' }} />
        </div>
      </div>

      {isConnected && chain ?
        <div className="row mt-3">
          <div className="col-12">
            <label htmlFor="formFile" className="form-label">
              <b>Step 2: Upload deposit data file</b>
            </label>
            <input type="file" className="form-control" id="formFile" />
            The deposit_data-[timestamp].json is located in your /staking-deposit-cli/validator_keys directory.
          </div>
        </div>
      : null}

    </div>
  );
}

export default VaultPage;
