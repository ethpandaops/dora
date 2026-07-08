import React, { useState } from 'react';
import { ConnectButton } from '@rainbow-me/rainbowkit';
import { useAccount } from 'wagmi';

import { ISubmitBuilderDepositsFormProps } from './SubmitBuilderDepositsFormProps';
import { IDeposit } from '../SubmitDepositsForm/DepositsTable';
import DepositGeneratorModal from '../SubmitDepositsForm/DepositGeneratorModal';
import BuilderDepositsTable from './BuilderDepositsTable';
import '../SubmitDepositsForm/SubmitDepositsForm.scss';

const SubmitBuilderDepositsForm = (props: ISubmitBuilderDepositsFormProps): React.ReactElement => {
  const { address: walletAddress, isConnected } = useAccount();

  const [file, setFile] = useState<File | null>(null);
  const [generatedDeposits, setGeneratedDeposits] = useState<IDeposit[] | null>(null);
  const [refreshIdx, setRefreshIdx] = useState<number>(0);
  const [showGeneratorModal, setShowGeneratorModal] = useState(false);

  return (
    <div className="submit-deposits">
      <div className="row">
        <div className="col-12">
          <h3>Submit builder deposits</h3>
          <p>This tool submits builder deposits to the builder deposit contract. Builder deposits carry a 0xB0 withdrawal credential and a proof-of-possession signed under the dedicated builder-deposit domain.</p>
          <div className="alert alert-warning">
            <b>Don't provide your keystore or mnemonic to us or any other website.</b> The generator below is for devnet testing only.
          </div>
        </div>
      </div>

      <div className="row mt-3">
        <div className="col-12"><b>Step 1: Connect your wallet</b></div>
      </div>
      <div className="row">
        <div className="col-12 p-2">
          <ConnectButton showBalance={true} accountStatus={{ smallScreen: 'avatar', largeScreen: 'full' }} chainStatus={{ smallScreen: 'icon', largeScreen: 'full' }} />
        </div>
      </div>

      <div className="row mt-3">
        <div className="col-12">
          <label htmlFor="formFile" className="form-label"><b>Step 2: Upload builder deposit data file</b></label>
          <div className="d-flex gap-2 align-items-center">
            <input
              type="file"
              className="form-control"
              onChange={(e: React.ChangeEvent<HTMLInputElement>) => {
                if (e.target.files) {
                  setFile(e.target.files[0]);
                  setGeneratedDeposits(null);
                  setRefreshIdx(refreshIdx + 1);
                }
              }}
            />
            <span className="text-muted">or</span>
            <button
              className="btn btn-outline-secondary text-nowrap"
              onClick={() => setShowGeneratorModal(true)}
              title="Generate builder deposits for devnet testing"
            >
              <i className="fa fa-magic me-1"></i>
              Generate
            </button>
          </div>
          <p className="text-secondary-emphasis mt-2">The deposit data file is a JSON array of builder deposits (pubkey, 0xB0 withdrawal_credentials, amount, signature).</p>
        </div>

        {(file || generatedDeposits) && isConnected && (
          <BuilderDepositsTable
            key={refreshIdx}
            file={file}
            deposits={generatedDeposits}
            genesisForkVersion={props.genesisForkVersion}
            builderDepositContract={props.builderDepositContract}
            explorerUrl={props.explorerUrl}
          />
        )}
        {(file || generatedDeposits) && !isConnected && (
          <div className="alert alert-info mt-2">Connect your wallet to review and submit the deposits.</div>
        )}
      </div>

      {showGeneratorModal && (
        <DepositGeneratorModal
          genesisForkVersion={props.genesisForkVersion}
          defaultWithdrawalAddress={walletAddress}
          domainType="builder"
          lockBuilderCredentials={true}
          onClose={() => setShowGeneratorModal(false)}
          onGenerate={(deposits) => {
            setGeneratedDeposits(deposits);
            setFile(null);
            setShowGeneratorModal(false);
            setRefreshIdx(refreshIdx + 1);
          }}
        />
      )}
    </div>
  );
};

export default SubmitBuilderDepositsForm;
