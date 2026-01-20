import React from 'react';
import { ConnectButton } from '@rainbow-me/rainbowkit';
import { useAccount } from 'wagmi';
import { useState } from 'react';

import { ISubmitDepositsFormProps } from './SubmitDepositsFormProps';
import DepositsTable from './DepositsTable';
import TopupDepositForm from './TopupDepositForm';
import { useGatingContract } from '../../hooks/useGatingContract';
import { GatingStatusBanner } from './GatingStatusBanner';
import GatingManageModal from './GatingManageModal';
import './SubmitDepositsForm.scss';

const SubmitDepositsForm = (props: ISubmitDepositsFormProps): React.ReactElement => {
  const { address: walletAddress, isConnected, chain } = useAccount();

  const [file, setFile] = useState<File | null>(null);
  const [refreshIdx, setRefreshIdx] = useState<number>(0);
  const [activeTab, setActiveTab] = useState<'initial' | 'topup'>('initial');
  const [showManageModal, setShowManageModal] = useState(false);

  // Fetch gating contract data
  const { gatingData, refetch: refetchGating, isLoading: isGatingLoading } = useGatingContract(
    props.depositContract,
    walletAddress,
    chain?.id
  );

  return (
    <div className="submit-deposits">
      <div className="row">
        <div className="col-12">
          <h3>Submit validator deposits</h3>
          <p>This tool can be used to submit validator deposits to the deposit contract.</p>
        </div>
      </div>

      {/* Tab navigation */}
      <div className="row">
        <div className="col-12 px-0">
          <ul className="nav nav-tabs">
            <li className="nav-item">
              <button
                className={`nav-link ${activeTab === 'initial' ? 'active' : ''}`}
                onClick={() => setActiveTab('initial')}
              >
                Initial Deposit
              </button>
            </li>
            <li className="nav-item">
              <button
                className={`nav-link ${activeTab === 'topup' ? 'active' : ''}`}
                onClick={() => setActiveTab('topup')}
              >
                Topup Deposit
              </button>
            </li>
          </ul>
        </div>
      </div>

      {activeTab === 'initial' && (
        <div className="row mt-3">
          <div className="col-12">
            <p>You can find instructions on how to generate deposits at the <a href="https://launchpad.ethereum.org/en/overview" target="_blank" rel="noreferrer">Staking Launchpad</a>.</p>
            <div className="alert alert-warning">
              <b>Don't provide your keystore or mnemonic to us or any other website</b>
            </div>
          </div>
        </div>
      )}

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

      {/* Gating Status Banner - only show on initial deposit tab since topup tab has its own */}
      {isConnected && (gatingData || isGatingLoading) && activeTab === 'initial' && (
        <div className="row mt-3">
          <div className="col-12">
            <GatingStatusBanner
              gatingData={gatingData}
              showDepositStatus={false}
              isLoading={isGatingLoading}
              onManageClick={() => setShowManageModal(true)}
            />
          </div>
        </div>
      )}

      {/* Initial Deposit Form */}
      {activeTab === 'initial' && (
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

          {file ?
            <DepositsTable
              key={refreshIdx}
              file={file}
              genesisForkVersion={props.genesisForkVersion}
              depositContract={props.depositContract}
              loadDepositTxs={props.loadDepositTxs}
              explorerUrl={props.explorerLink}
              gatingData={gatingData}
            />
          : null}
        </div>
      )}

      {/* Topup Deposit Form */}
      {activeTab === 'topup' && isConnected && (
        <TopupDepositForm
          loadValidators={props.loadValidators}
          searchValidators={props.searchValidators}
          depositContract={props.depositContract}
          maxEffectiveBalance={props.maxEffectiveBalance}
          maxEffectiveBalanceElectra={props.maxEffectiveBalanceElectra}
          gatingData={gatingData}
          isGatingLoading={isGatingLoading}
        />
      )}

      {/* Gating Management Modal */}
      {showManageModal && gatingData && chain && (
        <GatingManageModal
          gatingData={gatingData}
          chainId={chain.id}
          onClose={() => setShowManageModal(false)}
          onSuccess={() => refetchGating()}
        />
      )}
    </div>
  );
}

export default SubmitDepositsForm;
