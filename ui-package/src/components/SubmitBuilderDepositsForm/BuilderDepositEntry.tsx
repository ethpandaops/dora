import React, { useState } from 'react';
import { useAccount, useSendTransaction } from 'wagmi';
import { Modal } from 'react-bootstrap';

import { IDeposit } from '../SubmitDepositsForm/DepositsTable';
import { toReadableAmount } from '../../utils/ReadableAmount';

interface IBuilderDepositEntryProps {
  deposit: IDeposit;
  builderDepositContract: string;
  requestFee: bigint;
  explorerUrl?: string;
}

const GWEI = 1000000000n;

const BuilderDepositEntry = (props: IBuilderDepositEntryProps): React.ReactElement => {
  const { address, chain } = useAccount();
  const [errorModal, setErrorModal] = useState<string | null>(null);
  const submitRequest = useSendTransaction();

  const { deposit } = props;
  const pubkey = deposit.pubkey.replace(/^0x/, "");
  const wdCreds = deposit.withdrawal_credentials.replace(/^0x/, "");
  const signature = deposit.signature.replace(/^0x/, "");
  const amountValue = BigInt(deposit.amount) * GWEI; // stake (wei)
  const totalValue = amountValue + props.requestFee;

  return (
    <tr>
      <td className="text-truncate" style={{ maxWidth: "180px" }} title={"0x" + pubkey}>0x{pubkey}</td>
      <td className="text-truncate" style={{ maxWidth: "460px" }} title={"0x" + wdCreds}>0x{wdCreds}</td>
      <td>{toReadableAmount(deposit.amount, 9, "ETH", 9)}</td>
      <td>
        {deposit.validity ?
          <span className="badge bg-success">Valid</span>
        : <span className="badge bg-danger">Invalid</span>}
      </td>
      <td>
        <button
          className="btn btn-sm btn-primary"
          disabled={!deposit.validity || submitRequest.isPending || submitRequest.isSuccess}
          onClick={() => submitDeposit()}
        >
          {submitRequest.isSuccess ?
            <span><i className="fa fa-check me-1"></i>Sent</span>
          : submitRequest.isPending ?
            <span className="text-nowrap"><div className="spinner-border spinner-border-sm me-1" role="status"></div></span>
          : submitRequest.isError ?
            <span className="text-nowrap"><i className="fa-solid fa-repeat me-1"></i>Retry</span>
          : "Submit"}
        </button>
        {submitRequest.isSuccess && submitRequest.data ?
          <div className="small mt-1">
            {props.explorerUrl ?
              <a href={props.explorerUrl + "tx/" + submitRequest.data} target="_blank" rel="noreferrer">tx</a>
            : <span>{submitRequest.data}</span>}
          </div>
        : null}
        {errorModal && (
          <Modal show={true} onHide={() => setErrorModal(null)} size="lg">
            <Modal.Header closeButton>
              <Modal.Title>Builder Deposit Transaction Failed</Modal.Title>
            </Modal.Header>
            <Modal.Body><pre className="m-0">{errorModal}</pre></Modal.Body>
            <Modal.Footer>
              <button className="btn btn-primary" onClick={() => setErrorModal(null)}>Close</button>
            </Modal.Footer>
          </Modal>
        )}
      </td>
    </tr>
  );

  function submitDeposit() {
    // calldata (184 bytes): pubkey(48) ++ withdrawal_credentials(32) ++ amount(8, big-endian) ++ signature(96)
    // msg.value = stake (amount in wei) + predeploy queue fee. source is implicit (the deposit signature).
    const amountHex = deposit.amount.toString(16).padStart(16, "0");
    const data = ("0x" + pubkey + wdCreds + amountHex + signature) as `0x${string}`;

    submitRequest.sendTransactionAsync({
      to: props.builderDepositContract as `0x${string}`,
      account: address,
      chainId: chain?.id,
      value: totalValue,
      data,
      gas: 300000n,
    }).then(tx => {
      console.log(tx);
    }).catch(error => {
      setErrorModal(error.message);
    });
  }
};

export default BuilderDepositEntry;
