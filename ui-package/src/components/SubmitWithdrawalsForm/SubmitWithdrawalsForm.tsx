import React, { useEffect } from 'react';
import { ConnectButton } from '@rainbow-me/rainbowkit';
import { useAccount } from 'wagmi';
import { useState } from 'react';

import { ISubmitWithdrawalsFormProps, IValidator } from './SubmitWithdrawalsFormProps';
import ValidatorSelector, { formatBalance, formatStatus } from './ValidatorSelector';
import WithdrawalReview from './WithdrawalReview';

import './SubmitWithdrawalsForm.scss';
import { toReadableAmount } from '../../utils/ReadableAmount';

const SubmitWithdrawalsForm = (props: ISubmitWithdrawalsFormProps): React.ReactElement => {
  const { address: walletAddress, isConnected, chain } = useAccount();
  const [validators, setValidators] = useState<IValidator[] | null>(null);
  const [loadingError, setLoadingError] = useState<string | null>(null);
  const [validator, setValidator] = useState<IValidator | null>(null);
  const [withdrawalType, setWithdrawalType] = useState<number>(0);
  const [withdrawalAmount, setWithdrawalAmount] = useState<number>(0);
  useEffect(() => {
    if (walletAddress) {
      props.loadValidatorsCallback(walletAddress).then(setValidators).catch(setLoadingError);
    } else {
      setValidators(null)
    }
  }, [walletAddress, props.loadValidatorsCallback]);

  useEffect(() => {
    const timeoutId = window.setTimeout(() => {
      (window as any).explorer.initControls();
    }, 100);
    
    return () => {
      window.clearTimeout(timeoutId);
    };
  }, []);

  return (
    <div className="submit-deposits">
      <div className="row">
        <div className="col-12">
          <h3>Submit withdrawal requests</h3>
          <p>This tool can be used to create withdrawal requests for your validators.</p>
        </div>
      </div>

      <div className="row mt-3">
        <div className="col-12">
          <b>Step 1: Connect your wallet</b>
        </div>
      </div>
      <div className="row">
        <div className="col-12 p-2">
          <ConnectButton 
            showBalance={true} 
            accountStatus={{ smallScreen: 'avatar', largeScreen: 'full' }} 
            chainStatus={{ smallScreen: 'icon', largeScreen: 'full' }} 
          />
        </div>
      </div>

      {isConnected && chain && validators == null ?
        <div>
          Please wait while we load your validators...
        </div>
      : null}

      {isConnected && chain && loadingError ?
        <div className="alert alert-danger">
          <i className="fa fa-exclamation-triangle me-2"></i>
          Error loading validators: {loadingError.toString()}
        </div>
      : null}

      {isConnected && chain && !loadingError && validators !== null ?
        <>
          <div className="row mt-3">
            <div className="col-12">
              <label className="form-label">
                <b>Step 2: Select validator</b>
              </label>
            </div>
            <div className="col-12">
              <div className="form-text">
                Select the validator you want to exit or withdraw funds from.
              </div>
            </div>
            <div className="col-12 col-lg-11">
              <ValidatorSelector
                validators={validators}
                onChange={(validator) => {
                  setValidator(validator);
                  if(validator.credtype == "02") {
                    setWithdrawalType(0);
                    if(validator.balance > props.minValidatorBalance) {
                      setWithdrawalAmount(validator.balance - props.minValidatorBalance);
                    } else {
                      setWithdrawalAmount(1);
                    }
                  } else {
                    setWithdrawalType(1);
                    setWithdrawalAmount(1);
                  }
                }}
                value={validator}
              />
            </div>
          </div>

          {validator ?
            <div className="ms-2 mt-1">
              <div className="row">
                <div className="col-3 col-lg-2">
                  <b>Index:</b>
                </div>
                <div className="col-9 col-lg-10">
                  {validator.index}
                </div>
              </div>
              <div className="row">
                <div className="col-3 col-lg-2">
                  <b>Pubkey:</b>
                </div>
                <div className="col-9 col-lg-10">
                  <a href={`/validator/${validator.pubkey}`} target="_blank" rel="noreferrer">
                    {validator.pubkey}
                  </a>
                </div>
              </div>
              <div className="row">
                <div className="col-3 col-lg-2">
                  <b>Status:</b>
                </div>
                <div className="col-9 col-lg-10">
                  {formatStatus(validator.status)}
                </div>
              </div>
              <div className="row">
                <div className="col-3 col-lg-2">
                  <b>Balance:</b>
                </div>
                <div className="col-9 col-lg-10">
                  {toReadableAmount(validator.balance, 9, "LYX", 9)}
                </div>
              </div>
            </div>
          : null}

          {validator ?
            <>
              <div className="row mt-3">
                <div className="col-12">
                  <label className="form-label">
                    <b>Step 3: Select withdrawal amount</b>
                  </label>
                </div>
                <div className="col-12">
                  <div className="form-text">
                    Select the amount you want to withdraw. You may also decide to withdraw all funds from the validator and exit it from the validator set.
                  </div>
                </div>
                <div className="col-12">
                  <div className="form-check">
                    <input 
                      className="form-check-input" 
                      type="radio" 
                      name="withdrawalAmount" 
                      id="withdrawalAmountPartial" 
                      checked={withdrawalType == 0} 
                      onChange={() => setWithdrawalType(0)} 
                    />
                    <label className="form-check-label" htmlFor="withdrawalAmountPartial">
                      Partial withdrawal
                    </label>
                    {validator.credtype !== "02" ? 
                      <span 
                        className="text-warning ms-2" 
                        style={{fontSize: "0.9em"}} 
                        data-bs-toggle="tooltip" 
                        data-bs-placement="top" 
                        title="Partial withdrawals are only possible for validators with 0x02 withdrawal credentials."
                      >
                        <i className="fa fa-exclamation-triangle"></i>
                      </span>
                    : null}
                  </div>
                </div>
                <div className="col-12">
                  <div className="form-check">
                    <input 
                      className="form-check-input" 
                      type="radio" 
                      name="withdrawalAmount" 
                      id="withdrawalAmountFull" 
                      checked={withdrawalType == 1} 
                      onChange={() => setWithdrawalType(1)} 
                    />
                    <label className="form-check-label" htmlFor="withdrawalAmountFull">
                      Full withdrawal (Exit)
                    </label>
                  </div>
                </div>
              </div>
              {withdrawalType == 0 ?
                <>
                  <div className="row mt-3 withdrawal-details">
                    <div className="col-5 col-md-3 col-lg-2">
                      Validator Balance:
                    </div>
                    <div className="col-7 col-md-6 col-lg-4">
                      {toReadableAmount(validator.balance, 9, "LYX", 9)}
                    </div>
                  </div>
                  <div className="row mt-1 withdrawal-details">
                    <div className="col-5 col-md-3 col-lg-2">
                      Withdrawable Balance:
                    </div>
                    <div className="col-6 col-md-6 col-lg-4">
                      {toReadableAmount(validator.balance - props.minValidatorBalance, 9, "LYX", 9)}
                    </div>
                  </div>
                  <div className="row mt-1 withdrawal-details">
                    <div className="col-5 col-md-3 col-lg-2">
                      Requested Amount:
                    </div>
                    <div className="col-6 col-md-3 col-lg-2">
                      <input
                        type="number"
                        className="form-control"
                        min={0.000000001}
                        step={0.000000001}
                        value={toReadableAmount(withdrawalAmount, 9, "", 9)}
                        onChange={(e) => setWithdrawalAmount(Math.floor(parseFloat(e.target.value) * 1000000000))}
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
                        max={validator.balance - props.minValidatorBalance}
                        onChange={(evt) => setWithdrawalAmount(parseInt(evt.target.value))}
                        value={withdrawalAmount}
                      />
                    </div>
                  </div>
                </>
              : null}
            </>
          : null}

          {validator && ((withdrawalType == 0 && withdrawalAmount > 0) || withdrawalType == 1) ?
            <>
              <div className="row mt-3">
                <div className="col-12">
                  <label className="form-label">
                    <b>Step 4: Review & submit withdrawal request</b>
                  </label>
                </div>
              </div>
              <WithdrawalReview
                key={`${validator.index}-${withdrawalType}`}
                validator={validator}
                withdrawalAmount={withdrawalType == 0 ? withdrawalAmount : 0}
                withdrawalContract={props.withdrawalContract}
                explorerUrl={props.explorerUrl}
              />
            </>
          : null}
        </>
      : null}

    </div>
  );
}

export default SubmitWithdrawalsForm;
