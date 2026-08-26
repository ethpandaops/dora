import React, { useState, useMemo } from 'react';
import { useAccount } from 'wagmi';
import { mnemonicToAccount } from 'viem/accounts';

import { ISubmitBuilderDepositsFormProps } from './SubmitBuilderDepositsFormProps';
import { IDeposit } from '../SubmitDepositsForm/DepositsTable';
import DepositGeneratorModal from '../SubmitDepositsForm/DepositGeneratorModal';
import FaucetButton from '../SubmitDepositsForm/FaucetButton';
import BuilderDepositsTable from './BuilderDepositsTable';
import TopupBuilderForm from './TopupBuilderForm';
import LocalWalletBanner from '../SubmitShared/LocalWalletBanner';
import DepositSourcePanels from '../SubmitShared/DepositSourcePanels';
import { ConnectButton } from '@rainbow-me/rainbowkit';
import '../SubmitDepositsForm/SubmitDepositsForm.scss';

const SubmitBuilderDepositsForm = (props: ISubmitBuilderDepositsFormProps): React.ReactElement => {
  const { address: walletAddress, isConnected } = useAccount();

  const [file, setFile] = useState<File | null>(null);
  const [generatedDeposits, setGeneratedDeposits] = useState<IDeposit[] | null>(null);
  const [generatedMnemonic, setGeneratedMnemonic] = useState<string | null>(() => {
    // pick up the session's generated mnemonic so the wallet-free flow works
    // right after a reload, before regenerating deposits
    try {
      return sessionStorage.getItem('dora_generator_mnemonic');
    } catch {
      return null;
    }
  });
  const [refreshIdx, setRefreshIdx] = useState<number>(0);
  const [showGeneratorModal, setShowGeneratorModal] = useState(false);
  const [activeTab, setActiveTab] = useState<'initial' | 'topup'>('initial');

  // Without a connected wallet, deposits generated from a mnemonic can be submitted
  // from the wallet derived from that mnemonic (fund it via the faucet first).
  const localAccount = useMemo(() => {
    if (!generatedMnemonic) return null;
    try {
      return mnemonicToAccount(generatedMnemonic);
    } catch {
      return null;
    }
  }, [generatedMnemonic]);
  const useLocalAccount = !isConnected && localAccount !== null;

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

      {activeTab === 'topup' && (
        <>
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
          {useLocalAccount && (
            <LocalWalletBanner
              address={localAccount.address}
              faucetEnabled={props.faucetEnabled}
              faucetAmount={props.faucetAmount}
              explorerUrl={props.explorerUrl}
            />
          )}
          {(isConnected || useLocalAccount) && (
            <TopupBuilderForm
              builderDepositContract={props.builderDepositContract}
              loadBuilders={props.loadBuilders}
              searchBuilders={props.searchBuilders}
              explorerUrl={props.explorerUrl}
              localAccount={useLocalAccount ? localAccount : undefined}
            />
          )}
        </>
      )}

      {activeTab === 'initial' && (
      <>
      <DepositSourcePanels
        showGenerator={props.showGenerator !== false}
        fileLabel="Step 2: Upload builder deposit data file"
        fileHint="The deposit data file is a JSON array of builder deposits (pubkey, 0xB0 withdrawal_credentials, amount, signature)."
        generatorTitle="Generate builder deposits"
        generatorHint="Create and submit builder deposits without any external tooling:"
        walletExtra={isConnected && props.faucetEnabled ? (
          <FaucetButton
            address={walletAddress}
            amount={props.faucetAmount || 50}
            explorerUrl={props.explorerUrl}
          />
        ) : undefined}
        onFileSelected={(file) => {
          setFile(file);
          setGeneratedDeposits(null);
          setGeneratedMnemonic(null);
          setRefreshIdx(refreshIdx + 1);
        }}
        onGenerateClick={() => setShowGeneratorModal(true)}
      />

      <div className="row mt-3">
        {(file || generatedDeposits) && (isConnected || useLocalAccount) && (
          <>
            {useLocalAccount && (
              <LocalWalletBanner
                address={localAccount.address}
                faucetEnabled={props.faucetEnabled}
                faucetAmount={props.faucetAmount}
                explorerUrl={props.explorerUrl}
              />
            )}
            <BuilderDepositsTable
              key={refreshIdx}
              file={file}
              deposits={generatedDeposits}
              genesisForkVersion={props.genesisForkVersion}
              builderDepositContract={props.builderDepositContract}
              explorerUrl={props.explorerUrl}
              localAccount={useLocalAccount ? localAccount : undefined}
            />
          </>
        )}
        {(file || generatedDeposits) && !isConnected && !useLocalAccount && (
          <div className="alert alert-info mt-2">Connect your wallet to review and submit the deposits.</div>
        )}
      </div>
      </>
      )}

      {showGeneratorModal && (
        <DepositGeneratorModal
          genesisForkVersion={props.genesisForkVersion}
          defaultWithdrawalAddress={walletAddress}
          domainType="builder"
          lockBuilderCredentials={true}
          faucetEnabled={props.faucetEnabled}
          faucetAmount={props.faucetAmount}
          explorerUrl={props.explorerUrl}
          onClose={() => setShowGeneratorModal(false)}
          onGenerate={(deposits, mnemonic) => {
            setGeneratedDeposits(deposits);
            setGeneratedMnemonic(mnemonic);
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
