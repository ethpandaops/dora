import React, { useState, useEffect } from 'react';
import { useAccount, useWriteContract } from 'wagmi';
import { Modal } from 'react-bootstrap';
import { ContainerType, ByteVectorType, UintNumberType, ValueOf } from '@chainsafe/ssz';

import { IValidator } from '../SubmitConsolidationsForm/SubmitConsolidationsFormProps';
import ValidatorSelector from '../SubmitConsolidationsForm/ValidatorSelector';
import { formatBalance, formatStatus } from '../SubmitConsolidationsForm/ValidatorSelector';
import { DepositContractAbi } from './DepositContract';
import { toReadableAmount } from '../../utils/ReadableAmount';
import { topupRoot } from './TopUpRoot';

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
}

const TopupDepositForm = (props: ITopupDepositFormProps): React.ReactElement => {
  const { address: walletAddress, isConnected, chain } = useAccount();
  
  const [validators, setValidators] = useState<IValidator[]>([]);
  const [loadingError, setLoadingError] = useState<string | null>(null);
  const [selectedValidator, setSelectedValidator] = useState<IValidator | null>(null);
  const [topupAmount, setTopupAmount] = useState<number>(1); // UI input in LYX (float)
  const [topupAmountGwei, setTopupAmountGwei] = useState<bigint>(BigInt(1e9)); // Actual amount in Gwei (BigInt)
  const [maxTopupAmount, setMaxTopupAmount] = useState<number>(0); // UI max in LYX (float)
  const [walletBalance, setWalletBalance] = useState<bigint>(BigInt(100) * GWEI_PER_ETH); // Wallet balance in Gwei
  const [errorModal, setErrorModal] = useState<string | null>(null);

  // Parse max effective balance from props
  const maxEffectiveBalance = BigInt(props.maxEffectiveBalance);
  
  const maxEffectiveBalanceElectra = BigInt(props.maxEffectiveBalanceElectra);

  // Use wagmi's useWriteContract hook
  const topupRequest = useWriteContract();

  useEffect(() => {
    if (walletAddress && props.loadValidators) {
      // Load user's validators
      props.loadValidators(walletAddress).then(setValidators).catch(setLoadingError);
    }
  }, [walletAddress, props.loadValidators]);

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
      
      // Calculate max topup amount in LYX for UI (limited by wallet balance)
      const maxTopupEth = Number(remainingBalanceGwei / GWEI_PER_ETH);
      
      // Set max topup amount for UI slider/input
      setMaxTopupAmount(maxTopupEth);
      
      // Reset topup amount to 1 LYX when validator changes
      setTopupAmount(1);
      setTopupAmountGwei(GWEI_PER_ETH); // 1 LYX in Gwei
    }
  }, [selectedValidator, walletBalance, maxEffectiveBalance, maxEffectiveBalanceElectra]);

  const handleTopupSubmit = async () => {
    if (!selectedValidator || topupAmountGwei < GWEI_PER_ETH) return;
    
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

    // Submit transaction
    topupRequest.writeContractAsync({
      address: props.depositContract as `0x${string}`,
      account: walletAddress,
      abi: DepositContractAbi,
      chain: chain,
      functionName: "deposit",
      args: args,
      value: amountWei,
      gas: 150000n,
    }).then(tx => {
      console.log(tx);
    }).catch(error => {
      setErrorModal(error.message);
    });
  };

  const handleAmountInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const valueEth = parseFloat(e.target.value);
    if (!isNaN(valueEth) && valueEth >= 1 && valueEth <= maxTopupAmount) {
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

  return (
    <>
      <div className="row mt-3">
        <div className="col-12">
          <label className="form-label">
            <b>Step 2: Select validator to top up</b>
          </label>
        </div>
        <div className="col-12">
          <div className="form-text">
            Select the validator you want to add more {{ tokenSymbol }} to. The validator must be active on the network.
          </div>
        </div>
        <div className="col-12 col-lg-11">
          <ValidatorSelector
            placeholder="Select or search for a validator by index or pubkey"
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
                <a href={`/validator/${selectedValidator.pubkey}`} target="_blank" rel="noreferrer">
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
                {formatBalance(selectedValidator.balance, "{{ tokenSymbol }}")}
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
                Enter an amount of at least 1 {{ tokenSymbol }}. Maximum amount is limited by your wallet balance and the validator's remaining space up to the effective balance limit.
              </div>

              <div className="row mt-3 withdrawal-details">
                <div className="col-5 col-md-3 col-lg-2">
                  Max Effective Balance:
                </div>
                <div className="col-7 col-md-6 col-lg-4">
                  {toReadableAmount(Number(getMaxEffectiveBalance()), 9, "LYX", 0)}
                  {selectedValidator && selectedValidator.credtype !== "02" && (
                    <span 
                      className="text-info ms-2" 
                      style={{fontSize: "0.9em", cursor: "help"}} 
                      data-bs-toggle="tooltip" 
                      data-bs-placement="top" 
                      title={`This validator can be upgraded to the higher Electra limit of ${toReadableAmount(Number(maxEffectiveBalanceElectra), 9, "LYX", 0)} by switching to a compounding validator (0x02 credentials) via self-consolidation.`}
                    >
                      <i className="fa fa-info-circle"></i>
                    </span>
                  )}
                </div>
              </div>
              <div className="row mt-1 withdrawal-details">
                <div className="col-5 col-md-3 col-lg-2">
                  Max Topup Possible:
                </div>
                <div className="col-7 col-md-6 col-lg-4">
                  {toReadableAmount(Number(maxTopupAmount), 0, "LYX", 3)}
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
                    max={maxTopupAmount}
                    step={0.1}
                    value={topupAmount}
                    onChange={handleAmountInputChange}
                  />
                </div>
                <div className="col-1">
                  LYX
                </div>
                <div className="col-4 col-md-3 d-lg-none"></div>
                <div className="col-6 col-md-5 col-lg-3">
                  <input 
                    type="range" 
                    className="form-range"
                    min={1}
                    max={maxTopupAmount}
                    step={0.1}
                    onChange={handleSliderChange}
                    value={topupAmount}
                  />
                </div>
              </div>
              
              <div className="mt-3">
                <button 
                  className="btn btn-primary"
                  disabled={!selectedValidator || topupAmount < 1 || topupAmount > maxTopupAmount || topupRequest.isPending || topupRequest.isSuccess}
                  onClick={handleTopupSubmit}
                >
                  {topupRequest.isSuccess ?
                    <span>Submitted</span> :
                    topupRequest.isPending ? (
                      <span className="text-nowrap"><div className="spinner-border spinner-border-sm me-1" role="status"></div>Pending...</span>
                      ) : (
                        topupRequest.isError ? (
                          <span className="text-nowrap"><i className="fa-solid fa-repeat me-1"></i> Retry</span>
                        ) : (
                          "Submit Topup"
                        )
                      )
                  }
                </button>
                {topupAmount < 1 && (
                  <div className="text-danger mt-1">Amount must be at least 1 LYX</div>
                )}
                {topupAmount > maxTopupAmount && (
                  <div className="text-danger mt-1">Amount exceeds available limit</div>
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