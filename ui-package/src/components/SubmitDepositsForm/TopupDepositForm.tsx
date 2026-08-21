import React, { useState, useEffect } from 'react';
import { useAccount, useBalance, useConfig, useWriteContract } from 'wagmi';
import { createWalletClient, http } from 'viem';
import type { HDAccount } from 'viem';
import { Modal } from 'react-bootstrap';
import { ContainerType, ByteVectorType, UintNumberType, ValueOf } from '@chainsafe/ssz';

import { IValidator } from '../SubmitConsolidationsForm/SubmitConsolidationsFormProps';
import ValidatorSelector from '../SubmitConsolidationsForm/ValidatorSelector';
import { formatBalance, formatStatus } from '../SubmitConsolidationsForm/ValidatorSelector';
import { DepositContractAbi } from './DepositContract';
import { toReadableAmount } from '../../utils/ReadableAmount';
import { topupRoot } from './TopUpRoot';
import { GatingContractData, DEPOSIT_TYPES } from './GatingContract';
import { GatingStatusBanner } from './GatingStatusBanner';
import { useTrackedTx } from '../SubmitShared/useTrackedTx';
import TxStatusButton from '../SubmitShared/TxStatusButton';

// Define SSZ types for deposit data root calculation
const DepositMessage = new ContainerType({
  pubkey: new ByteVectorType(48),
  withdrawal_credentials: new ByteVectorType(32),
  amount: new UintNumberType(8),
});
type DepositMessage = ValueOf<typeof DepositMessage>;

// Constants for unit conversions
const GWEI_PER_ETH = BigInt(1e9);

interface ITopupDepositFormProps {
  loadValidators?: (address: string) => Promise<IValidator[]>;
  searchValidators?: (searchTerm: string) => Promise<IValidator[]>;
  depositContract: string;
  maxEffectiveBalance?: string;
  maxEffectiveBalanceElectra?: string;
  gatingData?: GatingContractData | null;
  isGatingLoading?: boolean;
  explorerUrl?: string;
  // Submit the transaction locally from this account instead of the connected wallet.
  localAccount?: HDAccount;

  // Generalization for the builder topup flow:
  entityName?: string; // 'validator' (default) or 'builder'
  entityLinkBase?: string; // '/validator/' (default) or '/builder/'
  // replaces the built-in deposit() submission (e.g. builder deposit calldata)
  customSubmit?: (pubkey: string, amountGwei: bigint) => Promise<string>;
  // entities without a max effective balance: hide the max-EB rows and cap by this
  maxTopupEthOverride?: number;
  extraFeeNote?: React.ReactNode;
}

const TopupDepositForm = (props: ITopupDepositFormProps): React.ReactElement => {
  const { address: walletAddress, chain: connectedChain } = useAccount();
  const wagmiConfig = useConfig();
  // without a connected wallet, fall back to the configured chain (local account mode)
  const chain = connectedChain ?? wagmiConfig.chains[0];
  
  const [validators, setValidators] = useState<IValidator[]>([]);
  const [loadingError, setLoadingError] = useState<string | null>(null);
  const [selectedValidator, setSelectedValidator] = useState<IValidator | null>(null);
  const [topupAmount, setTopupAmount] = useState<number>(1); // UI input in ETH (float)
  const [topupAmountGwei, setTopupAmountGwei] = useState<bigint>(BigInt(1e9)); // Actual amount in Gwei (BigInt)
  const [maxTopupAmount, setMaxTopupAmount] = useState<number>(0); // UI max in ETH (float)
  const [errorModal, setErrorModal] = useState<string | null>(null);

  // Parse max effective balance from props (absent for builders)
  const maxEffectiveBalance = BigInt(props.maxEffectiveBalance ?? '0');

  const maxEffectiveBalanceElectra = BigInt(props.maxEffectiveBalanceElectra ?? '0');

  const entityName = props.entityName ?? 'validator';
  const entityLinkBase = props.entityLinkBase ?? '/validator/';

  // Use wagmi's useWriteContract hook
  const topupRequest = useWriteContract();
  const tx = useTrackedTx(setErrorModal);

  // load validators owned by the connected wallet, or the generated wallet in local mode
  const ownerAddress = walletAddress ?? props.localAccount?.address;

  // funding wallet balance: a hard cap on the topup amount (minus ~0.1 ETH gas headroom)
  const ownerBalance = useBalance({
    address: ownerAddress as `0x${string}` | undefined,
    query: { refetchInterval: 12000 },
  });
  const walletCapEth = ownerBalance.data !== undefined
    ? Math.max(0, Math.floor((Number(ownerBalance.data.value) / 1e18 - 0.1) * 10000) / 10000)
    : null;
  const inputMaxEth = Math.max(1, walletCapEth !== null
    ? Math.min(Math.max(maxTopupAmount, 100), walletCapEth)
    : Math.max(maxTopupAmount, 100));
  useEffect(() => {
    if (ownerAddress && props.loadValidators) {
      // Load user's validators
      props.loadValidators(ownerAddress).then(setValidators).catch(setLoadingError);
    }
  }, [ownerAddress, props.loadValidators]);

  // Initialize tooltips
  useEffect(() => {
    // Check if we're in a browser environment (window exists)
    if (typeof window !== 'undefined' && selectedValidator) {
      // Initialize Bootstrap tooltips
      const tooltipTriggerList = document.querySelectorAll('[data-bs-toggle="tooltip"]');
      
      // Use the type assertion to access bootstrap property
      const bootstrapInstance = (window as any).bootstrap;
      
      if (bootstrapInstance && tooltipTriggerList.length > 0) {
        Array.from(tooltipTriggerList).forEach(tooltipTriggerEl => {
          new bootstrapInstance.Tooltip(tooltipTriggerEl);
        });
      } else {
        // Fallback for when bootstrap isn't available in the window
        setTimeout(() => {
          // This fallback assumes a global function might be available to initialize tooltips
          if (typeof (window as any).explorer !== 'undefined' && (window as any).explorer.initControls) {
            (window as any).explorer.initControls();
          }
        }, 100);
      }
    }
  }, [selectedValidator]);

  useEffect(() => {
    if (selectedValidator) {
      if (props.maxTopupEthOverride !== undefined) {
        // no max effective balance for this entity type (builders): cap externally
        setMaxTopupAmount(props.maxTopupEthOverride);
      } else {
        // Determine max effective balance based on validator's withdrawal credential type
        const effectiveMaxBalance = selectedValidator.credtype === "02"
          ? maxEffectiveBalanceElectra
          : maxEffectiveBalance;

        // Convert validator balance to BigInt
        const validatorBalanceGwei = BigInt(selectedValidator.balance);

        // Calculate remaining balance in Gwei
        const remainingBalanceGwei = effectiveMaxBalance > validatorBalanceGwei
          ? effectiveMaxBalance - validatorBalanceGwei
          : BigInt(0);

        // Calculate max topup amount in ETH for UI (limited by wallet balance)
        const maxTopupEth = Number(remainingBalanceGwei / GWEI_PER_ETH);

        // Set max topup amount for UI slider/input
        setMaxTopupAmount(maxTopupEth);
      }

      // Reset topup amount to 1 ETH when validator changes
      setTopupAmount(1);
      setTopupAmountGwei(GWEI_PER_ETH); // 1 ETH in Gwei
    }
  }, [selectedValidator, maxEffectiveBalance, maxEffectiveBalanceElectra, props.maxTopupEthOverride]);

  const handleTopupSubmit = async () => {
    if (!selectedValidator || topupAmountGwei < GWEI_PER_ETH) return;

    // custom submission path (builder topups use raw builder-deposit calldata)
    if (props.customSubmit) {
      const pubkey = selectedValidator.pubkey;
      tx.start(() => props.customSubmit(pubkey, topupAmountGwei));
      return;
    }

    console.log(selectedValidator.pubkey, topupAmountGwei);
    const hashTreeRoot = await topupRoot(selectedValidator.pubkey, topupAmountGwei);
    const depositDataRoot = '0x' + Array.from(hashTreeRoot)
      .map(byte => byte.toString(16).padStart(2, '0'))
      .join('');

    // Prepare arguments for deposit contract call
    const args = [
      selectedValidator.pubkey,
      "0x0000000000000000000000000000000000000000000000000000000000000000",
      "0x000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000",
      depositDataRoot
    ];

    // Calculate amount in wei (gwei * 10^9)
    const amountWei = topupAmountGwei * BigInt(10 ** 9);

    // Submit transaction. No explicit gas limit: let estimation decide -
    // gas-repriced devnets (glamsterdam) need far more than the classic costs.
    tx.start(() => {
      if (props.localAccount) {
        // no connected wallet: sign & send locally with the mnemonic-derived account
        const walletClient = createWalletClient({
          account: props.localAccount,
          chain,
          transport: http(),
        });
        return walletClient.writeContract({
          address: props.depositContract as `0x${string}`,
          abi: DepositContractAbi,
          functionName: "deposit",
          args: args,
          value: amountWei,
        });
      }
      return topupRequest.writeContractAsync({
        address: props.depositContract as `0x${string}`,
        account: walletAddress,
        abi: DepositContractAbi,
        chain: chain,
        functionName: "deposit",
        args: args,
        value: amountWei,
      });
    });
  };

  const handleAmountInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const valueEth = parseFloat(e.target.value);
    // amounts beyond the effective-balance room are allowed (devnet testing) - the
    // UI warns that the excess is of no use instead of blocking the input
    if (!isNaN(valueEth) && valueEth >= 1) {
      // Update UI display value
      setTopupAmount(valueEth);
      
      // Convert to Gwei and store as BigInt
      const valueGwei = BigInt(Math.floor(valueEth * 1e9));
      setTopupAmountGwei(valueGwei);
    }
  };

  const handleSliderChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const valueEth = parseFloat(e.target.value);
    setTopupAmount(valueEth);
    
    // Convert to Gwei and store as BigInt
    const valueGwei = BigInt(Math.floor(valueEth * 1e9));
    setTopupAmountGwei(valueGwei);
  };

  // Get the appropriate max effective balance based on validator's credential type
  const getMaxEffectiveBalance = (): bigint => {
    if (!selectedValidator) return maxEffectiveBalance;
    return selectedValidator.credtype === "02" ? maxEffectiveBalanceElectra : maxEffectiveBalance;
  };

  // Check gating status for topup deposits
  const topupConfig = props.gatingData?.depositConfigs.get(DEPOSIT_TYPES.TOPUP);
  const isTopupBlocked = topupConfig?.blocked ?? false;
  const topupRequiresToken = !(topupConfig?.noToken ?? true);
  const hasToken = (props.gatingData?.tokenBalance ?? 0n) > 0n;
  const canSubmitTopup = !isTopupBlocked && (!topupRequiresToken || hasToken);

  return (
    <>
      {/* Gating Status for Topup Deposits */}
      {(props.gatingData || props.isGatingLoading) && (
        <div className="row mt-3">
          <div className="col-12">
            <GatingStatusBanner
              gatingData={props.gatingData ?? null}
              depositType={DEPOSIT_TYPES.TOPUP}
              showDepositStatus={true}
              isLoading={props.isGatingLoading}
            />
          </div>
        </div>
      )}

      <div className="row mt-3">
        <div className="col-12">
          <label className="form-label">
            <b>Step 2: Select {entityName} to top up</b>
          </label>
        </div>
        <div className="col-12">
          <div className="form-text">
            Select the {entityName} you want to add more ETH to. The {entityName} must be active on the network.
          </div>
        </div>
        <div className="col-12 col-lg-11">
          <ValidatorSelector
            placeholder={`Select or search for a ${entityName} by index or pubkey`}
            validators={validators}
            onChange={setSelectedValidator}
            value={selectedValidator}
            isLazyLoaded={true}
            searchValidatorsCallback={props.searchValidators}
          />
        </div>
      </div>
      
      {selectedValidator && (
        <>
          <div className="ms-2 mt-1">
            <div className="row">
              <div className="col-3 col-lg-2">
                <b>Index:</b>
              </div>
              <div className="col-9 col-lg-10">
                {selectedValidator.index}
              </div>
            </div>
            <div className="row">
              <div className="col-3 col-lg-2">
                <b>Pubkey:</b>
              </div>
              <div className="col-9 col-lg-10">
                <a href={`${entityLinkBase}${selectedValidator.pubkey}`} target="_blank" rel="noreferrer">
                  {selectedValidator.pubkey}
                </a>
              </div>
            </div>
            <div className="row">
              <div className="col-3 col-lg-2">
                <b>Status:</b>
              </div>
              <div className="col-9 col-lg-10">
                {formatStatus(selectedValidator.status)}
              </div>
            </div>
            <div className="row">
              <div className="col-3 col-lg-2">
                <b>Balance:</b>
              </div>
              <div className="col-9 col-lg-10">
                {formatBalance(selectedValidator.balance, "ETH")}
              </div>
            </div>
            <div className="row">
              <div className="col-3 col-lg-2">
                <b>Withdrawal Credentials:</b>
              </div>
              <div className="col-9 col-lg-10">
                <span className={`badge rounded-pill ${selectedValidator.credtype === '02' ? 'bg-success' : 'bg-warning'}`}>
                  0x{selectedValidator.credtype}
                </span>
              </div>
            </div>
          </div>

          <div className="row mt-3">
            <div className="col-12">
              <label className="form-label">
                <b>Step 3: Select withdrawal amount</b>
              </label>
            </div>
            <div className="col-12">
              <div className="form-text">
                Enter an amount of at least 1 ETH. Maximum amount is limited by your wallet balance and the validator's remaining space up to the effective balance limit.
              </div>

              {props.extraFeeNote && (
                <div className="row mt-3 withdrawal-details">
                  <div className="col-5 col-md-3 col-lg-2">
                    Queue fee:
                  </div>
                  <div className="col-7 col-md-6 col-lg-4">
                    {props.extraFeeNote}
                  </div>
                </div>
              )}
              {props.maxTopupEthOverride === undefined && (
              <div className="row mt-3 withdrawal-details">
                <div className="col-5 col-md-3 col-lg-2">
                  Max Effective Balance:
                </div>
                <div className="col-7 col-md-6 col-lg-4">
                  {toReadableAmount(Number(getMaxEffectiveBalance()), 9, "ETH", 0)}
                  {selectedValidator && selectedValidator.credtype !== "02" && (
                    <span 
                      className="text-info ms-2" 
                      style={{fontSize: "0.9em", cursor: "help"}} 
                      data-bs-toggle="tooltip" 
                      data-bs-placement="top" 
                      title={`This validator can be upgraded to the higher Electra limit of ${toReadableAmount(Number(maxEffectiveBalanceElectra), 9, "ETH", 0)} by switching to a compounding validator (0x02 credentials) via self-consolidation.`}
                    >
                      <i className="fa fa-info-circle"></i>
                    </span>
                  )}
                </div>
              </div>
              )}
              <div className="row mt-1 withdrawal-details">
                <div className="col-5 col-md-3 col-lg-2">
                  Max Topup Possible:
                </div>
                <div className="col-7 col-md-6 col-lg-4">
                  {toReadableAmount(Number(maxTopupAmount), 0, "ETH", 3)}
                </div>
              </div>
              <div className="row mt-1 withdrawal-details">
                <div className="col-5 col-md-3 col-lg-2">
                  Topup Amount:
                </div>
                <div className="col-6 col-md-3 col-lg-2">
                  <input
                    type="number"
                    className="form-control"
                    id="topupAmount"
                    min={1}
                    max={inputMaxEth}
                    step={0.1}
                    value={topupAmount}
                    onChange={handleAmountInputChange}
                  />
                </div>
                <div className="col-1">
                  ETH
                </div>
                <div className="col-4 col-md-3 d-lg-none"></div>
                <div className="col-6 col-md-5 col-lg-3">
                  <input
                    type="range"
                    className="form-range"
                    min={1}
                    max={inputMaxEth}
                    step={0.1}
                    onChange={handleSliderChange}
                    value={topupAmount}
                  />
                </div>
              </div>
              
              <div className="mt-3">
                <TxStatusButton
                  tx={tx}
                  onSubmit={handleTopupSubmit}
                  disabled={!selectedValidator || topupAmount < 1 || (walletCapEth !== null && topupAmount > walletCapEth) || !canSubmitTopup}
                  idleLabel={
                    isTopupBlocked ? (
                      <span className="text-nowrap"><i className="fa fa-ban me-1"></i> Blocked</span>
                    ) : topupRequiresToken && !hasToken ? (
                      <span className="text-nowrap"><i className="fa fa-lock me-1"></i> Token Required</span>
                    ) : (
                      "Submit Topup"
                    )
                  }
                  confirmedLabel="Submitted"
                  explorerUrl={props.explorerUrl}
                />
                {topupAmount < 1 && (
                  <div className="text-danger mt-1">Amount must be at least 1 ETH</div>
                )}
                {walletCapEth !== null && topupAmount > walletCapEth && (
                  <div className="text-danger mt-1">
                    Amount exceeds the funding wallet's balance ({walletCapEth} ETH usable after gas headroom)
                  </div>
                )}
                {props.maxTopupEthOverride === undefined && topupAmount > maxTopupAmount && topupAmount <= (walletCapEth ?? Infinity) && (
                  <div className="text-warning mt-1">
                    <i className="fa fa-exclamation-triangle me-1"></i>
                    Exceeds the {entityName}'s remaining effective-balance room ({maxTopupAmount} ETH)
                    {selectedValidator.credtype !== '02' && ' - the excess will be swept back to the withdrawal credentials'}
                  </div>
                )}
              </div>
            </div>
          </div>
        </>
      )}
      
      {errorModal && (
        <Modal show={true} onHide={() => setErrorModal(null)} size="lg" className="submit-deposit-modal">
          <Modal.Header closeButton>
            <Modal.Title>Deposit Transaction Failed</Modal.Title>
          </Modal.Header>
          <Modal.Body>
            <pre className="m-0 deposit-error">{errorModal}</pre>
          </Modal.Body>
          <Modal.Footer>
            <button className="btn btn-primary" onClick={() => setErrorModal(null)}>Close</button>
          </Modal.Footer>
        </Modal>
      )}
    </>
  );
};

export default TopupDepositForm; 