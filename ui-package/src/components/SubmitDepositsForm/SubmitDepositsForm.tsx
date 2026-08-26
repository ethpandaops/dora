import React from 'react';
import { ConnectButton } from '@rainbow-me/rainbowkit';
import { useAccount } from 'wagmi';
import { useState, useMemo } from 'react';
import { mnemonicToAccount } from 'viem/accounts';

import { ISubmitDepositsFormProps } from './SubmitDepositsFormProps';
import DepositsTable, { IDeposit } from './DepositsTable';
import TopupDepositForm from './TopupDepositForm';
import { useGatingContract } from '../../hooks/useGatingContract';
import { GatingStatusBanner } from './GatingStatusBanner';
import GatingManageModal from './GatingManageModal';
import DepositGeneratorModal from './DepositGeneratorModal';
import FaucetButton from './FaucetButton';
import LocalWalletBanner from '../SubmitShared/LocalWalletBanner';
import DepositSourcePanels from '../SubmitShared/DepositSourcePanels';
import './SubmitDepositsForm.scss';

const SubmitDepositsForm = (props: ISubmitDepositsFormProps): React.ReactElement => {
  const { address: walletAddress, isConnected, chain } = useAccount();

  const [file, setFile] = useState<File | null>(null);
  const [generatedDeposits, setGeneratedDeposits] = useState<IDeposit[] | null>(null);
  const [generatedMnemonic, setGeneratedMnemonic] = useState<string | null>(() => {
    // pick up the session's generated mnemonic so the wallet-free flow (incl. the
    // topup tab) works right after a reload, before regenerating deposits
    try {
      return sessionStorage.getItem('dora_generator_mnemonic');
    } catch {
      return null;
    }
  });
  const [refreshIdx, setRefreshIdx] = useState<number>(0);
  const [activeTab, setActiveTab] = useState<'initial' | 'topup'>('initial');
  const [showManageModal, setShowManageModal] = useState(false);
  const [showGeneratorModal, setShowGeneratorModal] = useState(false);

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

      {/* Initial tab: two-column source chooser (wallet & file vs. generator).
          Topup tab keeps a plain connect-wallet row. */}
      {activeTab === 'initial' && (
        <DepositSourcePanels
          showGenerator={props.showGenerator !== false}
          fileLabel="Step 2: Upload deposit data file"
          fileHint={<span>The deposit data file is usually called <code>deposit_data-[timestamp].json</code> and is located in your <code>/staking-deposit-cli/validator_keys</code> directory.</span>}
          generatorTitle="Generate validator deposits"
          generatorHint="Create and submit validator deposits without any external tooling:"
          walletExtra={isConnected && props.faucetEnabled ? (
            <FaucetButton
              address={walletAddress}
              amount={props.faucetAmount || 50}
              explorerUrl={props.explorerLink}
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
      )}
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
        </>
      )}

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
          {(file || generatedDeposits) && useLocalAccount && (
            <LocalWalletBanner
              address={localAccount.address}
              faucetEnabled={props.faucetEnabled}
              faucetAmount={props.faucetAmount}
              explorerUrl={props.explorerLink}
            />
          )}
          {(file || generatedDeposits) && (
            <DepositsTable
              key={refreshIdx}
              file={file}
              deposits={generatedDeposits}
              genesisForkVersion={props.genesisForkVersion}
              depositContract={props.depositContract}
              loadDepositTxs={props.loadDepositTxs}
              explorerUrl={props.explorerLink}
              gatingData={gatingData}
              localAccount={useLocalAccount ? localAccount : undefined}
            />
          )}
        </div>
      )}

      {/* Topup Deposit Form */}
      {activeTab === 'topup' && useLocalAccount && (
        <LocalWalletBanner
          address={localAccount.address}
          faucetEnabled={props.faucetEnabled}
          faucetAmount={props.faucetAmount}
          explorerUrl={props.explorerLink}
        />
      )}
      {activeTab === 'topup' && (isConnected || useLocalAccount) && (
        <TopupDepositForm
          loadValidators={props.loadValidators}
          searchValidators={props.searchValidators}
          depositContract={props.depositContract}
          maxEffectiveBalance={props.maxEffectiveBalance}
          maxEffectiveBalanceElectra={props.maxEffectiveBalanceElectra}
          gatingData={gatingData}
          isGatingLoading={isGatingLoading}
          explorerUrl={props.explorerLink}
          localAccount={useLocalAccount ? localAccount : undefined}
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

      {/* Deposit Generator Modal */}
      {showGeneratorModal && (
        <DepositGeneratorModal
          genesisForkVersion={props.genesisForkVersion}
          defaultWithdrawalAddress={walletAddress}
          faucetEnabled={props.faucetEnabled}
          faucetAmount={props.faucetAmount}
          explorerUrl={props.explorerLink}
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
}

export default SubmitDepositsForm;
