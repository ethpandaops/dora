import React, { useRef } from 'react';
import { useAccount, useSendTransaction } from 'wagmi';
import { useState } from 'react';
import { IValidator } from './SubmitConsolidationsFormProps';
import { toReadableAmount } from '../../utils/ReadableAmount';
import { Modal } from 'react-bootstrap';
import { useQueueDataCache } from '../../hooks/useQueueDataCache';

interface IConsolidationReviewProps {
  sourceValidator: IValidator;
  targetValidator: IValidator;
  consolidationContract: string;
  explorerUrl: string;
}


const ConsolidationReview = (props: IConsolidationReviewProps) => {
  const { address, chain } = useAccount();
  const [addExtraFee, setAddExtraFee] = useState(true);
  const [errorModal, setErrorModal] = useState<string | null>(null);
  const componentRef = useRef<HTMLDivElement>(null);

  const logLookbackRange = 10;
  
  const submitRequest = useSendTransaction();
  const { queueData, logData: cachedLogData, refetch: refetchQueueData, isLoading: cacheLoading } = useQueueDataCache(props.consolidationContract, chain?.id);

  let queueLength = 0n;
  let avgRequestPerBlock = 0;
  let isPreElectra = false;
  let requiredFee = 0n;
  let requestFee = 0n;
  let failedQueueLength = false;
  let isLoading = cacheLoading;
  let error: Error | null = null;
  
  if (queueData) {
    isLoading = queueData.isLoading;
    error = queueData.error;
    
    if (!queueData.error && !queueData.isLoading) {
      queueLength = queueData.queueLength;
      
      if (queueLength === 0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffn) {
        isPreElectra = true;
      } else {
        requiredFee = getRequiredFee(queueLength);

        if(addExtraFee && cachedLogData) {
          // add extra fee to avoid rejection due to other submissions
          for(let block in cachedLogData.logCount) {
            avgRequestPerBlock += cachedLogData.logCount[block];
          }
          avgRequestPerBlock /= logLookbackRange;

          let extraFeeForRequest = avgRequestPerBlock;
          if (extraFeeForRequest < 2) {
            extraFeeForRequest = 3;
          } else {
            extraFeeForRequest++;
          }

          requestFee = getRequiredFee(queueLength + BigInt(Math.ceil(extraFeeForRequest)));
        } else {
          requestFee = requiredFee;
        }
      }
    } else if (error) {
      failedQueueLength = true;
    }
  }

  var feeFactor = 0;
  var feeUnit = "Wei";

  if (requestFee > 100000000000000n) {
    feeFactor = 18;
    feeUnit = "LYX";
  } else if (requestFee > 100000n) {
    feeFactor = 9;
    feeUnit = "Gwei";
  }

  return (
    <div ref={componentRef}>
      {error ?
        <div className="alert alert-danger" role="alert">
          Error loading queue length from consolidation contract. <br />
          {error?.message} <br />
          <button className="btn btn-primary mt-2" onClick={() => refetchQueueData()}>
            Retry
          </button>
        </div>
      : failedQueueLength ?
        <div className="alert alert-danger" role="alert">
          Error loading queue length from consolidation contract. (check contract address: {props.consolidationContract})
        </div>
      : isLoading ?
        <p>Loading...</p>
      : isPreElectra ?
        <div className="alert alert-danger" role="alert">
          The network is not on Electra yet, so consolidation requests can not be submitted.
        </div>
      : <div>
          {!props.sourceValidator.isconsolidable && (
            <div className="alert alert-warning" role="alert">
              <i className="fa-solid fa-triangle-exclamation me-2"></i>
              This consolidation will fail because the source validator is not withdrawable yet. The validator must be withdrawable before it can be consolidated.
            </div>
          )}
          {props.targetValidator.credtype !== "02" && props.targetValidator.index != props.sourceValidator.index && (
            <div className="alert alert-warning" role="alert">
              <i className="fa-solid fa-triangle-exclamation me-2"></i>
              This consolidation will fail because the target validator does not have 0x02 withdrawal credentials. The target validator must first perform a self-consolidation to update its withdrawal credentials to 0x02.
            </div>
          )}
          <div className="row">
            <div className="col-3 col-lg-2">
              Consolidation Contract:
            </div>
            <div className="col-9 col-lg-10">
              {props.consolidationContract}
            </div>
          </div>
          <div className="row">
            <div className="col-3 col-lg-2">
              Consolidation Queue:
            </div>
            <div className="col-9 col-lg-10">
              {queueLength.toString()} Consolidations
            </div>
          </div>
          <div className="row">
            <div className="col-3 col-lg-2">
              Required queue fee:
            </div>
            <div className="col-9 col-lg-10">
              {toReadableAmount(requiredFee, feeFactor, feeUnit, 4)}
            </div>
          </div>
          <div className="row">
            <div className="col-3 col-lg-2">
              Add extra fee:
            </div>
            <div className="col-9 col-lg-10">
              <input type="checkbox" className="form-check-input" id="addExtraFee" checked={addExtraFee} onChange={(e) => setAddExtraFee(e.target.checked)} />
              <label htmlFor="addExtraFee" className="ms-1">Add extra fee to avoid rejection due to other submissions</label>
            </div>
          </div>
          {addExtraFee &&
          <div className="row">
            <div className="col-3 col-lg-2">
              Avg. requests per block:
            </div>
            <div className="col-9 col-lg-10">
              {avgRequestPerBlock.toFixed(2)}  (last {logLookbackRange} blocks)
            </div>
          </div>
          }
          {addExtraFee &&
          <div className="row">
            <div className="col-3 col-lg-2">
              Extra fee:
            </div>
            <div className="col-9 col-lg-10">
              {toReadableAmount(requestFee - requiredFee, feeFactor, feeUnit, 4)}
            </div>
          </div>
          }
          <div className="row">
            <div className="col-3 col-lg-2">
              Total fee:
            </div>
            <div className="col-9 col-lg-10">
              {toReadableAmount(requestFee, feeFactor, feeUnit, 4)}
            </div>
          </div>
          <div className="row mt-3">
            <div className="col-12">
              <button 
                className="btn btn-primary" 
                disabled={submitRequest.isPending || submitRequest.isSuccess} 
                onClick={() => submitConsolidation()}
              >
                {submitRequest.isSuccess ?
                  <span>Submitted</span> :
                  submitRequest.isPending ? (
                    <span className="text-nowrap"><div className="spinner-border spinner-border-sm me-1" role="status"></div>Pending...</span>
                    ) : (
                      submitRequest.isError ? (
                        <span className="text-nowrap"><i className="fa-solid fa-repeat me-1"></i> Retry consolidation</span>
                      ) : (
                        "Submit consolidation request"
                      )
                    )
                }
              </button>
            </div>
          </div>
          {submitRequest.isSuccess ?
            <div className="row mt-3">
              <div className="col-12">
                <div className="alert alert-success">
                  Consolidation TX: 
                  {props.explorerUrl ?
                    <a className="ms-1" href={props.explorerUrl + "tx/" + submitRequest.data} target="_blank" rel="noreferrer">{submitRequest.data}</a>
                  : <span className="ms-1">{submitRequest.data}</span>
                  }
                </div>
              </div>
            </div>
          : null}
        </div>
        }
        {errorModal && (
          <Modal show={true} onHide={() => setErrorModal(null)} size="lg">
            <Modal.Header closeButton>
              <Modal.Title>Consolidation Transaction Failed</Modal.Title>
            </Modal.Header>
            <Modal.Body>
              <pre className="m-0">{errorModal}</pre>
            </Modal.Body>
            <Modal.Footer>
              <button className="btn btn-primary" onClick={() => setErrorModal(null)}>Close</button>
            </Modal.Footer>
          </Modal>
        )}
    </div>
  );

  function getRequiredFee(numerator: bigint): bigint {
    // https://eips.ethereum.org/EIPS/eip-7251#fee-calculation
    let i = 1n;
    let output = 0n;
    let numeratorAccum = 1n * 17n; // factor * denominator

    while (numeratorAccum > 0n) {
        output += numeratorAccum;
        numeratorAccum = (numeratorAccum * numerator) / (17n * i);
        i += 1n;
    }

    return output / 17n;
  }

  function submitConsolidation() {
    submitRequest.sendTransactionAsync({
      to: props.consolidationContract,
      account: address,
      chainId: chain?.id,
      value: requestFee,
      // https://eips.ethereum.org/EIPS/eip-7251#add-consolidation-request
      // calldata (96 bytes): sourceValidator.pubkey (48 bytes) + targetValidator.pubkey (48 bytes)
      data: "0x" + props.sourceValidator.pubkey.substring(2) + props.targetValidator.pubkey.substring(2),
      gas: 200000n,
    }).then(tx => {
      console.log(tx);
    }).catch(error => {
      setErrorModal(error.message);
    });

  }

};

export default ConsolidationReview;
