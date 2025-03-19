import React from 'react';
import { useAccount, useWriteContract } from 'wagmi';
import { useState } from 'react';
import { Modal } from 'react-bootstrap';

import { IDeposit } from './DepositsTable';
import { toReadableAmount } from '../../utils/ReadableAmount';

interface IDepositEntryProps {
  deposit: IDeposit;
  depositContract: string;
  explorerUrl?: string;
}

const DepositContractAbi = [{"inputs":[],"stateMutability":"nonpayable","type":"constructor"},{"anonymous":false,"inputs":[{"indexed":false,"internalType":"bytes","name":"pubkey","type":"bytes"},{"indexed":false,"internalType":"bytes","name":"withdrawal_credentials","type":"bytes"},{"indexed":false,"internalType":"bytes","name":"amount","type":"bytes"},{"indexed":false,"internalType":"bytes","name":"signature","type":"bytes"},{"indexed":false,"internalType":"bytes","name":"index","type":"bytes"}],"name":"DepositEvent","type":"event"},{"inputs":[{"internalType":"bytes","name":"pubkey","type":"bytes"},{"internalType":"bytes","name":"withdrawal_credentials","type":"bytes"},{"internalType":"bytes","name":"signature","type":"bytes"},{"internalType":"bytes32","name":"deposit_data_root","type":"bytes32"}],"name":"deposit","outputs":[],"stateMutability":"payable","type":"function"},{"inputs":[],"name":"get_deposit_count","outputs":[{"internalType":"bytes","name":"","type":"bytes"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"get_deposit_root","outputs":[{"internalType":"bytes32","name":"","type":"bytes32"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"bytes4","name":"interfaceId","type":"bytes4"}],"name":"supportsInterface","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"pure","type":"function"}];

const DepositEntry = (props: IDepositEntryProps): React.ReactElement => {
  const { address: walletAddress, chain, isConnected } = useAccount();
  const [errorModal, setErrorModal] = useState<string | null>(null);
  const [showTxDetails, setShowTxDetails] = useState<boolean>(false);

  const depositRequest = useWriteContract();
  window.setTimeout(() => {
    (window as any).explorer.initControls();
  }, 100);
  
  return (
    <tr>
      <td style={{maxWidth: "550px"}}>
        <div className="ellipsis-copy-btn">
          <i className="fa fa-copy text-muted p-1" role="button" data-bs-toggle="tooltip" title="Copy to clipboard" data-clipboard-text={props.deposit.pubkey}></i>
        </div>
        0x{props.deposit.pubkey}
      </td>
      <td style={{maxWidth: "460px"}}>
        <div className="ellipsis-copy-btn">
          <i className="fa fa-copy text-muted p-1" role="button" data-bs-toggle="tooltip" title="Copy to clipboard" data-clipboard-text={props.deposit.withdrawal_credentials}></i>
        </div>
        {formatWithdrawalHash(props.deposit.withdrawal_credentials)}
      </td>
      <td>
        {formatAmount(props.deposit.amount, chain?.nativeCurrency?.symbol || "ETH")}
        {props.deposit.amount > 32000000000 && props.deposit.withdrawal_credentials.substring(0, 2) === "01" && (
          <span className="text-warning ms-2" style={{fontSize: "0.9em"}} data-bs-toggle="tooltip" data-bs-placement="top" title="You're trying to submit a validator key with >32 ETH and 0x01 withdrawal credentials. Please note, that your validator will be running with a max effective balance of 32 ETH. The excess Balance will almost immediately be withdrawn.">
            <i className="fa fa-exclamation-triangle"></i>
          </span>
        )}
      </td>
      <td>
        <span data-bs-toggle="tooltip" data-bs-placement="top" title={props.deposit.validity ? "The deposit signature is valid for this ethereum network." : "The deposit signature is invalid for this ethereum network."}>
          {props.deposit.validity ? 
            <span className="text-success">✅</span> : 
            <span className="text-danger">❌</span>
          }
        </span>
        {props.deposit.depositTxs.length > 0 ? 
          <a className="text-warning ms-2" href="#" data-bs-toggle="tooltip" data-bs-placement="top" title="This pubkey has already been submitted to the deposit contract. Click to see more." onClick={() => setShowTxDetails(true)}>
            <i className="fa fa-exclamation-triangle"></i>
          </a>
        : null}
      </td>
      <td className="p-0">
        <button className="btn btn-primary" disabled={!isConnected || !props.deposit.validity || depositRequest.isPending || depositRequest.isSuccess} onClick={() => submitDeposit()}>
          {depositRequest.isSuccess ?
            <span>Submitted</span> :
            depositRequest.isPending ? (
              <span className="text-nowrap"><div className="spinner-border spinner-border-sm me-1" role="status"></div>Pending...</span>
              ) : (
                depositRequest.isError ? (
                  <span className="text-nowrap"><i className="fa-solid fa-repeat me-1"></i> Retry</span>
                ) : (
                  "Submit"
                )
              )
          }
        </button>
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
        {showTxDetails && (
          <Modal show={true} onHide={() => setShowTxDetails(false)} size="lg" className="deposit-txs-modal">
            <Modal.Header closeButton>
              <Modal.Title>Initiated Deposits</Modal.Title>
            </Modal.Header>
            <Modal.Body>
              <p>Some deposits have already been submitted for this validator:</p>
              {props.deposit.depositTxs.map((tx, index) => (
                <div key={index + "-" + tx.tx_hash} className="mt-2">
                  {index > 0 && <hr />}
                  <div className="d-flex">
                    <div className="tx-details-label">
                      Tx Hash:
                    </div>
                    <div className="tx-details-value">
                      <div className="d-flex">
                        {props.explorerUrl ?
                          <a className="flex-grow-1 text-truncate" href={props.explorerUrl + "tx/" + tx.tx_hash} target="_blank" rel="noreferrer">{tx.tx_hash}</a>
                        : <span className="flex-grow-1 text-truncate">{tx.tx_hash}</span>
                        }
                        <div className="ms-2">
                          <i className="fa fa-copy text-muted p-1" role="button" data-bs-toggle="tooltip" data-clipboard-text={tx.tx_hash} title="Copy to clipboard"></i>
                        </div>
                      </div>
                    </div>
                  </div>
                  <div className="d-flex">
                    <div className="tx-details-label">
                      Block:
                    </div>
                    <div className="tx-details-value">
                      <div className="d-flex">
                        <span className="flex-grow-1 text-truncate">{tx.block}</span>
                        <div className="ms-2">
                          <i className="fa fa-copy text-muted p-1" role="button" data-bs-toggle="tooltip" data-clipboard-text={tx.block} title="Copy to clipboard"></i>
                        </div>
                      </div>
                    </div>
                  </div>
                  <div className="d-flex">
                    <div className="tx-details-label">
                      Block Time:
                    </div>
                    <div className="tx-details-value">
                      <div className="d-flex">
                        <span className="flex-grow-1 text-truncate">{(window as any).explorer.renderRecentTime(tx.block_time)}</span>
                        <div className="ms-2">
                          <i className="fa fa-copy text-muted p-1" role="button" data-bs-toggle="tooltip" data-clipboard-text={new Date(tx.block_time * 1000).toISOString()} title="Copy to clipboard"></i>
                        </div>
                      </div>
                    </div>
                  </div>
                  <div className="d-flex">
                    <div className="tx-details-label">
                      TX Origin:
                    </div>
                    <div className="tx-details-value">
                      <div className="d-flex">
                        {props.explorerUrl ?
                          <a className="flex-grow-1 text-truncate" href={props.explorerUrl + "address/" + tx.tx_origin} target="_blank" rel="noreferrer">{tx.tx_origin}</a>
                        : <span className="flex-grow-1 text-truncate">{tx.tx_origin}</span>
                        }
                        <div className="ms-2">
                          <i className="fa fa-copy text-muted p-1" role="button" data-bs-toggle="tooltip" data-clipboard-text={tx.tx_origin} title="Copy to clipboard"></i>
                        </div>
                      </div>
                    </div>
                  </div>
                  <div className="d-flex">
                    <div className="tx-details-label">
                      TX Target:  
                    </div>
                    <div className="tx-details-value">
                      <div className="d-flex">
                        {props.explorerUrl ?
                          <a className="flex-grow-1 text-truncate" href={props.explorerUrl + "address/" + tx.tx_target} target="_blank" rel="noreferrer">{tx.tx_target}</a>
                        : <span className="flex-grow-1 text-truncate">{tx.tx_target}</span>
                        }
                        <div className="ms-2">
                          <i className="fa fa-copy text-muted p-1" role="button" data-bs-toggle="tooltip" data-clipboard-text={tx.tx_target} title="Copy to clipboard"></i>
                        </div>
                      </div>
                    </div>
                  </div>
                  <div className="d-flex">
                    <div className="tx-details-label">
                      Amount:  
                    </div>
                    <div className="tx-details-value">
                      <div className="d-flex">
                        <span className="flex-grow-1 text-truncate">{toReadableAmount(tx.amount, 9, "ETH", 0)}</span>
                      </div>
                    </div>
                  </div>
                </div>
              ))}
            </Modal.Body>
            <Modal.Footer>
              <button className="btn btn-primary" onClick={() => setErrorModal(null)}>Close</button>
            </Modal.Footer>
          </Modal>
        )}

      </td>
    </tr>
  );

  function formatWithdrawalHash(creds: string) {
    switch (creds.substring(0, 2)) {
      case "02":
        return <span><span className="text-success">02</span>...{creds.substring(24)}</span>;
      case "01":
        return <span><span className="text-success">01</span>...{creds.substring(24)}</span>;
      default:
        return <span><span className="text-warning">{creds.substring(0, 2)}</span>{creds.substring(2)}</span>;
    }
  }

  function formatAmount(amount: number, ethSymbol: string) {
    let amountEth = amount / 1e9;
    return amountEth.toFixed(0) + " " + ethSymbol;
  }

  async function submitDeposit() {
    let args = [ "0x" + props.deposit.pubkey, "0x" + props.deposit.withdrawal_credentials, "0x" + props.deposit.signature, "0x" + props.deposit.deposit_data_root ];
    depositRequest.writeContractAsync({
      address: props.depositContract as `0x${string}`,
      account: walletAddress,
      abi: DepositContractAbi,
      chain: chain,
      functionName: "deposit",
      args: args,
      value: BigInt(props.deposit.amount) * BigInt(10 ** 9),
      gas: 150000n,
    }).then(tx => {
      console.log(tx);
    }).catch(error => {
      setErrorModal(error.message);
    });
  }
}

export default DepositEntry;
