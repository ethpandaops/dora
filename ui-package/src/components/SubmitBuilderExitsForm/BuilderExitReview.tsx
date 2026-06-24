import React, { useRef, useState } from 'react';
import { useAccount, useSendTransaction } from 'wagmi';
import { Modal } from 'react-bootstrap';

import { toReadableAmount } from '../../utils/ReadableAmount';
import { useQueueDataCache } from '../../hooks/useQueueDataCache';

interface IBuilderExitReviewProps {
  pubkey: string;
  builderExitContract: string;
  explorerUrl: string;
}

const BuilderExitReview = (props: IBuilderExitReviewProps) => {
  const { address, chain } = useAccount();
  const [addExtraFee, setAddExtraFee] = useState(true);
  const [errorModal, setErrorModal] = useState<string | null>(null);
  const componentRef = useRef<HTMLDivElement>(null);

  const logLookbackRange = 10;

  const submitRequest = useSendTransaction();
  const { queueData, logData: cachedLogData, refetch: refetchQueueData, isLoading: cacheLoading } = useQueueDataCache(props.builderExitContract, chain?.id);

  let queueLength = 0n;
  let avgRequestPerBlock = 0;
  let isPreFork = false;
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
        isPreFork = true;
      } else {
        requiredFee = getRequiredFee(queueLength);

        if (addExtraFee && cachedLogData) {
          for (let block in cachedLogData.logCount) {
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

  let feeFactor = 0;
  let feeUnit = "Wei";
  if (requestFee > 100000000000000n) {
    feeFactor = 18;
    feeUnit = "ETH";
  } else if (requestFee > 100000n) {
    feeFactor = 9;
    feeUnit = "Gwei";
  }

  return (
    <div ref={componentRef}>
      {error ?
        <div className="alert alert-danger" role="alert">
          Error loading queue length from builder exit contract. <br />
          {error?.message} <br />
          <button className="btn btn-primary mt-2" onClick={() => refetchQueueData()}>Retry</button>
        </div>
      : isLoading ?
        <p>Loading...</p>
      : failedQueueLength ?
        <div className="alert alert-danger" role="alert">
          Error loading queue length from builder exit contract. (check contract address: {props.builderExitContract})
        </div>
      : isPreFork ?
        <div className="alert alert-danger" role="alert">
          The network is not on Gloas yet, so builder exit requests can not be submitted.
        </div>
      : <div>
          <div className="row">
            <div className="col-3 col-lg-2">Builder Exit Contract:</div>
            <div className="col-9 col-lg-10">{props.builderExitContract}</div>
          </div>
          <div className="row">
            <div className="col-3 col-lg-2">Exit Queue:</div>
            <div className="col-9 col-lg-10">{queueLength.toString()} Exits</div>
          </div>
          <div className="row">
            <div className="col-3 col-lg-2">Required queue fee:</div>
            <div className="col-9 col-lg-10">{toReadableAmount(requiredFee, feeFactor, feeUnit, 4)}</div>
          </div>
          <div className="row">
            <div className="col-3 col-lg-2">Add extra fee:</div>
            <div className="col-9 col-lg-10">
              <input type="checkbox" className="form-check-input" id="addExtraFee" checked={addExtraFee} onChange={(e) => setAddExtraFee(e.target.checked)} />
              <label htmlFor="addExtraFee" className="ms-1">Add extra fee to avoid rejection due to other submissions</label>
            </div>
          </div>
          {addExtraFee &&
          <div className="row">
            <div className="col-3 col-lg-2">Avg. requests per block:</div>
            <div className="col-9 col-lg-10">{avgRequestPerBlock.toFixed(2)} (last {logLookbackRange} blocks)</div>
          </div>}
          {addExtraFee &&
          <div className="row">
            <div className="col-3 col-lg-2">Extra fee:</div>
            <div className="col-9 col-lg-10">{toReadableAmount(requestFee - requiredFee, feeFactor, feeUnit, 4)}</div>
          </div>}
          <div className="row">
            <div className="col-3 col-lg-2">Total fee:</div>
            <div className="col-9 col-lg-10">{toReadableAmount(requestFee, feeFactor, feeUnit, 4)}</div>
          </div>
          <div className="row mt-3">
            <div className="col-12">
              <button className="btn btn-primary" disabled={submitRequest.isPending || submitRequest.isSuccess} onClick={() => submitExit()}>
                {submitRequest.isSuccess ?
                  <span>Submitted</span> :
                  submitRequest.isPending ?
                    <span className="text-nowrap"><div className="spinner-border spinner-border-sm me-1" role="status"></div>Pending...</span>
                  : submitRequest.isError ?
                    <span className="text-nowrap"><i className="fa-solid fa-repeat me-1"></i> Retry exit</span>
                  : "Submit exit request"}
              </button>
            </div>
          </div>
          {submitRequest.isSuccess ?
            <div className="row mt-3">
              <div className="col-12">
                <div className="alert alert-success">
                  Exit TX:
                  {props.explorerUrl ?
                    <a className="ms-1" href={props.explorerUrl + "tx/" + submitRequest.data} target="_blank" rel="noreferrer">{submitRequest.data}</a>
                  : <span className="ms-1">{submitRequest.data}</span>}
                </div>
              </div>
            </div>
          : null}
        </div>
      }
      {errorModal && (
        <Modal show={true} onHide={() => setErrorModal(null)} size="lg">
          <Modal.Header closeButton>
            <Modal.Title>Builder Exit Transaction Failed</Modal.Title>
          </Modal.Header>
          <Modal.Body><pre className="m-0">{errorModal}</pre></Modal.Body>
          <Modal.Footer>
            <button className="btn btn-primary" onClick={() => setErrorModal(null)}>Close</button>
          </Modal.Footer>
        </Modal>
      )}
    </div>
  );

  function getRequiredFee(numerator: bigint): bigint {
    // https://eips.ethereum.org/EIPS/eip-7002#fee-calculation (shared predeploy fee mechanism)
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

  function submitExit() {
    submitRequest.sendTransactionAsync({
      to: props.builderExitContract as `0x${string}`,
      account: address,
      chainId: chain?.id,
      value: requestFee,
      // calldata (48 bytes): builder pubkey. source_address = msg.sender.
      data: ("0x" + props.pubkey.replace(/^0x/, "")) as `0x${string}`,
      gas: 200000n,
    }).then(tx => {
      console.log(tx);
    }).catch(error => {
      setErrorModal(error.message);
    });
  }
};

export default BuilderExitReview;
