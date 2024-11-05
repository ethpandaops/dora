import React from 'react';
import { ConnectButton } from '@rainbow-me/rainbowkit';
import { useAccount } from 'wagmi';
import { useState } from 'react';

import { ISubmitDepositsFormProps } from './SubmitDepositsFormProps';
import DepositsTable from './DepositsTable';
import './SubmitDepositsForm.scss';

const SubmitDepositsForm = (props: ISubmitDepositsFormProps): React.ReactElement => {
  const { address: walletAddress, isConnected, chain } = useAccount();
  
  const [file, setFile] = useState<File | null>(null);
  const [refreshIdx, setRefreshIdx] = useState<number>(0);


  return (
    <div className="submit-deposits">
      <div className="row">
        <div className="col-12">
          <h3>Submit validator deposits</h3>
          <p>This tool can be used to submit validator deposits to the deposit contract.</p>
          <p>You can find instructions on how to generate deposits at the <a href="https://launchpad.ethereum.org/en/overview" target="_blank" rel="noreferrer">Staking Launchpad</a>.</p>
          <div className="alert alert-warning">
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

      <div className="row mt-3">
        <div className="col-12">
          <label htmlFor="formFile" className="form-label">
            <b>Step 2: Upload deposit data file</b>
          </label>
          <input 
            type="file" 
            className="form-control" 
            onChange={(e: React.ChangeEvent<HTMLInputElement>) => {
              if (e.target.files) {
                setFile(e.target.files[0]);
                setRefreshIdx(refreshIdx + 1);
              }
            }} 
          />
          <p className="text-secondary-emphasis mt-2">The deposit data file is usually called <code>deposit_data-[timestamp].json</code> and is located in your <code>/staking-deposit-cli/validator_keys</code> directory.</p>
        </div>
      </div>

      {file ?
        <DepositsTable
          key={refreshIdx}
          file={file}
          genesisForkVersion={props.genesisForkVersion}
          depositContract={props.depositContract}
          loadDepositTxs={props.loadDepositTxs}
        />
      : null}

    </div>
  );
}

export default SubmitDepositsForm;
