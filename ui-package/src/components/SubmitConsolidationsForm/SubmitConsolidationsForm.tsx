import React, { useEffect } from 'react';
import { ConnectButton } from '@rainbow-me/rainbowkit';
import { useAccount } from 'wagmi';
import { useState } from 'react';

import { ISubmitConsolidationsFormProps, IValidator } from './SubmitConsolidationsFormProps';
import ValidatorSelector, { formatBalance, formatStatus } from './ValidatorSelector';
import ConsolidationReview from './ConsolidationReview';

import './SubmitConsolidationsForm.scss';

const SubmitConsolidationsForm = (props: ISubmitConsolidationsFormProps): React.ReactElement => {
  const { address: walletAddress, isConnected, chain } = useAccount();
  const [validators, setValidators] = useState<IValidator[]>(null);
  const [loadingError, setLoadingError] = useState<string | null>(null);
  const [sourceValidator, setSourceValidator] = useState<IValidator | null>(null);
  const [targetValidator, setTargetValidator] = useState<IValidator | null>(null);

  useEffect(() => {
    if (walletAddress) {
      props.loadValidatorsCallback(walletAddress).then(setValidators).catch(setLoadingError);
    } else {
      setValidators(null)
    }
  }, [walletAddress]);

  return (
    <div className="submit-deposits">
      <div className="row">
        <div className="col-12">
          <h3>Submit consolidation requests</h3>
          <p>This tool can be used to create consolidation requests for your validators.</p>
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
                <b>Step 2: Select source validator</b>
              </label>
            </div>
            <div className="col-12">
              <div className="form-text">
                Select the validator you want to consolidate your funds from. This validator will be exited and its funds will be sent to the target validator.
              </div>
            </div>
            <div className="col-12 col-lg-11">
              <ValidatorSelector
                placeholder="Select a validator"
                validators={validators}
                onChange={(validator) => {
                  console.log("source validator", validator);
                  setSourceValidator(validator);
                }}
                value={sourceValidator}
              />
            </div>
          </div>

          {sourceValidator ?
            <div className="ms-2 mt-1">
              <div className="row">
                <div className="col-3 col-lg-2">
                  <b>Index:</b>
                </div>
                <div className="col-9 col-lg-10">
                  {sourceValidator.index}
                </div>
              </div>
              <div className="row">
                <div className="col-3 col-lg-2">
                  <b>Pubkey:</b>
                </div>
                <div className="col-9 col-lg-10">
                  <a href={`/validator/${sourceValidator.pubkey}`} target="_blank" rel="noreferrer">
                    {sourceValidator.pubkey}
                  </a>
                </div>
              </div>
              <div className="row">
                <div className="col-3 col-lg-2">
                  <b>Status:</b>
                </div>
                <div className="col-9 col-lg-10">
                  {formatStatus(sourceValidator.status)}
                </div>
              </div>
              <div className="row">
                <div className="col-3 col-lg-2">
                  <b>Balance:</b>
                </div>
                <div className="col-9 col-lg-10">
                  {formatBalance(sourceValidator.balance, "{{ tokenSymbol }}")}
                </div>
              </div>
            </div>
          : null}

          <div className="row mt-3">
            <div className="col-12">
              <label className="form-label">
                <b>Step 3: Select target validator</b>
              </label>
            </div>
            <div className="col-12">
              <div className="form-text">
                Select the validator you want to consolidate your funds to. This validator will receive the funds from the source validator and gets 0x02 withdrawal credentials assigned.
              </div>
            </div>
            <div className="col-12 col-lg-11">
              <ValidatorSelector
                placeholder="Select or search for a validator by index or pubkey"
                validators={validators}
                onChange={(validator) => {
                  console.log("target validator", validator);
                  setTargetValidator(validator);
                }}
                value={targetValidator}
                isLazyLoaded={true}
                searchValidatorsCallback={props.searchValidatorsCallback}
              />
            </div>
          </div>
          {targetValidator ?
            <div className="ms-2 mt-1">
              <div className="row">
                <div className="col-3 col-lg-2">
                  <b>Index:</b>
                </div>
                <div className="col-9 col-lg-10">
                  {targetValidator.index}
                </div>
              </div>
              <div className="row">
                <div className="col-3 col-lg-2">
                  <b>Pubkey:</b>
                </div>
                <div className="col-9 col-lg-10">
                  <a href={`/validator/${targetValidator.pubkey}`} target="_blank" rel="noreferrer">
                    {targetValidator.pubkey}
                  </a>
                </div>
              </div>
              <div className="row">
                <div className="col-3 col-lg-2">
                  <b>Status:</b>
                </div>
                <div className="col-9 col-lg-10">
                  {formatStatus(targetValidator.status)}
                </div>
              </div>
              <div className="row">
                <div className="col-3 col-lg-2">
                  <b>Balance:</b>
                </div>
                <div className="col-9 col-lg-10">
                  {formatBalance(targetValidator.balance, "{{ tokenSymbol }}")}
                </div>
              </div>
            </div>
          : null}


          {sourceValidator && targetValidator ?
            <>
              <div className="row mt-3">
                <div className="col-12">
                  <label className="form-label">
                    <b>Step 4: Review & submit consolidation request</b>
                  </label>
                </div>
              </div>
              <ConsolidationReview
                key={`${sourceValidator.index}-${targetValidator.index}`}
                sourceValidator={sourceValidator}
                targetValidator={targetValidator}
                consolidationContract={props.consolidationContract}
                explorerUrl={props.explorerUrl}
              />
            </>
          : null}
        </>
      : null}

    </div>
  );
}

export default SubmitConsolidationsForm;
