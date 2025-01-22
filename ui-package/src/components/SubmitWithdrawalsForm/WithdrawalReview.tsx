import React, { useEffect } from 'react';
import { useAccount, useSendTransaction, useBlockNumber, usePublicClient } from 'wagmi';
import { useStorageAt } from 'wagmi'
import { useState } from 'react';
import { IValidator } from './SubmitWithdrawalsFormProps';
import { toReadableAmount } from '../../utils/ReadableAmount';
import { Modal } from 'react-bootstrap';

interface IWithdrawalReviewProps {
  validator: IValidator;
  withdrawalAmount: number;
  withdrawalContract: string;
  explorerUrl: string;
}

interface IWithdrawalLogState {
  lastBlock: bigint;
  logCount: {[block: string]: number};
}

const WithdrawalReview = (props: IWithdrawalReviewProps) => {
  const { address, chain } = useAccount();
  const [logState, setLogState] = useState<IWithdrawalLogState>({
    lastBlock: 0n,
    logCount: {},
  });
  const [addExtraFee, setAddExtraFee] = useState(true);
  const [errorModal, setErrorModal] = useState<string | null>(null);

  const blockNumber = useBlockNumber();
  const client = usePublicClient({
    chainId: chain?.id,
  });

  const logLookbackRange = 10;

  useEffect(() => {
    // log loader
    if (!blockNumber.isFetched || !client || !addExtraFee) 
      return;

    if (!blockNumber.data || blockNumber.data <= logState.lastBlock) 
      return;

    let fromBlock = blockNumber.data - BigInt(logLookbackRange);
    if (fromBlock < 0n) 
      fromBlock = 0n;

    let cancelUpdate = false;
    let logsPromise = client.request({
      method: 'eth_getLogs',
      params: [
        {
          address: props.withdrawalContract,
          fromBlock: "0x"+fromBlock.toString(16),
        },
      ],
    });
    logsPromise.then((logs) => {
      if (cancelUpdate) 
        return;

      let newLogState: IWithdrawalLogState = {
        lastBlock: blockNumber.data,
        logCount: {},
      };

      (logs as any[]).forEach((log) => {
        let blockNum = BigInt(log.blockNumber);
        let blockStr = blockNum.toString();
        if (!newLogState.logCount[blockStr]) 
          newLogState.logCount[blockStr] = 0;
        newLogState.logCount[blockStr]++;
      });

      setLogState(newLogState);
    });

    return function() {
      cancelUpdate = true;
    }
  }, [blockNumber, client, addExtraFee]);

  const withdrawalQueueLengthCall = useStorageAt({
    address: props.withdrawalContract as `0x${string}`,
    slot: "0x00",
    chainId: chain?.id,
	});

  const submitRequest = useSendTransaction();

  useEffect(() => {
    const interval = setInterval(() => {
      withdrawalQueueLengthCall.refetch();
      blockNumber.refetch();
    }, 15000);
    return () => {
      clearInterval(interval);
    };
  }, []);

  let queueLength = 0n;
  let avgRequestPerBlock = 0;
  let isPreElectra = false;
  let requiredFee = 0n;
  let requestFee = 0n;
  let failedQueueLength = false;
  if (withdrawalQueueLengthCall.isFetched && withdrawalQueueLengthCall.data) {
    var queueLenHex = withdrawalQueueLengthCall.data as string;
    if (!queueLenHex) {
      failedQueueLength = true;
    } else if (queueLenHex == "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff") {
      isPreElectra = true;
    } else {
      queueLength = BigInt(queueLenHex);
      requiredFee = getRequiredFee(queueLength);

      if(addExtraFee) {
        // add extra fee to avoid rejection due to other submissions
        for(let block in logState.logCount) {
          avgRequestPerBlock += logState.logCount[block];
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
  }

  var feeFactor = 0;
  var feeUnit = "Wei";

  if (requestFee > 100000000000000n) {
    feeFactor = 18;
    feeUnit = "ETH";
  } else if (requestFee > 100000n) {
    feeFactor = 9;
    feeUnit = "Gwei";
  }

  return (
    <div>
      {withdrawalQueueLengthCall.isError ?
        <div className="alert alert-danger" role="alert">
          Error loading queue length from withdrawal contract. <br />
          {withdrawalQueueLengthCall.error?.message} <br />
          <button className="btn btn-primary mt-2" onClick={() => withdrawalQueueLengthCall.refetch()}>
            Retry
          </button>
        </div>
      : !withdrawalQueueLengthCall.isFetched ?
        <p>Loading...</p>
      : failedQueueLength ?
        <div className="alert alert-danger" role="alert">
          Error loading queue length from withdrawal contract. (check contract address: {props.withdrawalContract})
        </div>
      : isPreElectra ?
        <div className="alert alert-danger" role="alert">
          The network is not on Electra yet, so withdrawal requests can not be submitted.
        </div>
      : <div>
          <div className="row">
            <div className="col-3 col-lg-2">
              Withdrawal Contract:
            </div>
            <div className="col-9 col-lg-10">
              {props.withdrawalContract}
            </div>
          </div>
          <div className="row">
            <div className="col-3 col-lg-2">
              Withdrawal Queue:
            </div>
            <div className="col-9 col-lg-10">
              {queueLength.toString()} Withdrawals
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
              <button className="btn btn-primary" disabled={submitRequest.isPending || submitRequest.isSuccess} onClick={() => submitWithdrawal()}>
                {submitRequest.isSuccess ?
                  <span>Submitted</span> :
                  submitRequest.isPending ? (
                    <span className="text-nowrap"><div className="spinner-border spinner-border-sm me-1" role="status"></div>Pending...</span>
                    ) : (
                      submitRequest.isError ? (
                        <span className="text-nowrap"><i className="fa-solid fa-repeat me-1"></i> Retry withdrawal</span>
                      ) : (
                        "Submit " + (props.withdrawalAmount > 0 ? "withdrawal" : "exit") + " request"
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
                  Withdrawal TX: 
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
              <Modal.Title>Withdrawal Transaction Failed</Modal.Title>
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
    // https://eips.ethereum.org/EIPS/eip-7002#fee-calculation
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

  function submitWithdrawal() {
    submitRequest.sendTransactionAsync({
      to: props.withdrawalContract,
      account: address,
      chainId: chain?.id,
      value: requestFee,
      // https://eips.ethereum.org/EIPS/eip-7002#add-withdrawal-request
      // calldata (56 bytes): sourceValidator.pubkey (48 bytes) + amount (8 bytes)
      data: "0x" + props.validator.pubkey.substring(2) + props.withdrawalAmount.toString(16).padStart(16, "0"),
      gas: 200000n,
    }).then(tx => {
      console.log(tx);
    }).catch(error => {
      setErrorModal(error.message);
    });

  }

};

export default WithdrawalReview;
