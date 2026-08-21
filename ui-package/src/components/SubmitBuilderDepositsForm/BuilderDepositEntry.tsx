import React, { useState } from 'react';
import { useAccount, useConfig, useSendTransaction } from 'wagmi';
import { createWalletClient, http } from 'viem';
import type { HDAccount } from 'viem';
import { Modal } from 'react-bootstrap';

import { IDeposit } from '../SubmitDepositsForm/DepositsTable';
import { toReadableAmount } from '../../utils/ReadableAmount';
import { useEnsLookup } from '../SubmitShared/useEnsLookup';
import { credentialTypeInfo } from '../SubmitShared/credentialTypes';
import { useTrackedTx } from '../SubmitShared/useTrackedTx';
import TxStatusButton from '../SubmitShared/TxStatusButton';

interface IBuilderDepositEntryProps {
  deposit: IDeposit;
  builderDepositContract: string;
  requestFee: bigint;
  explorerUrl?: string;
  // Submit the transaction locally from this account instead of the connected wallet.
  localAccount?: HDAccount;
}

const GWEI = 1000000000n;

const BuilderDepositEntry = (props: IBuilderDepositEntryProps): React.ReactElement => {
  const { address, chain: connectedChain } = useAccount();
  const wagmiConfig = useConfig();
  const chain = connectedChain ?? wagmiConfig.chains[0];
  const [errorModal, setErrorModal] = useState<string | null>(null);
  const submitRequest = useSendTransaction();
  const tx = useTrackedTx(setErrorModal);

  const { deposit } = props;
  const pubkey = deposit.pubkey.replace(/^0x/, "");
  const wdCreds = deposit.withdrawal_credentials.replace(/^0x/, "");
  const signature = deposit.signature.replace(/^0x/, "");
  const amountValue = BigInt(deposit.amount) * GWEI; // stake (wei)
  const totalValue = amountValue + props.requestFee;

  // ENS name of the address embedded in the credentials (0xb0/0x01/0x02 types)
  const credsAddress = /^(b0|01|02)/i.test(wdCreds) ? "0x" + wdCreds.substring(24) : null;
  const credsEnsName = useEnsLookup(credsAddress);
  const credsPrefixClass = credentialTypeInfo(wdCreds.substring(0, 2)).className;

  return (
    <tr>
      <td className="text-truncate" style={{ maxWidth: "180px" }} title={"0x" + pubkey}>0x{pubkey}</td>
      {/* elide the 11 zero-padding bytes: 0xb0…<address or its ENS name> (full value in tooltip) */}
      <td className="text-truncate" style={{ maxWidth: "460px" }} title={"0x" + wdCreds + (credsAddress ? ` (${credsAddress})` : '')}>
        {credsAddress ? (
          <span>0x<span className={credsPrefixClass}>{wdCreds.substring(0, 2)}</span>…{credsEnsName ?? credsAddress.substring(2)}</span>
        ) : (
          <span>0x<span className={credsPrefixClass}>{wdCreds.substring(0, 2)}</span>{wdCreds.substring(2)}</span>
        )}
      </td>
      <td>{toReadableAmount(deposit.amount, 9, "ETH", 9)}</td>
      <td>
        {deposit.validity ?
          <span className="badge bg-success">Valid</span>
        : <span className="badge bg-danger">Invalid</span>}
      </td>
      <td>
        <TxStatusButton
          small
          tx={tx}
          onSubmit={() => submitDeposit()}
          disabled={!deposit.validity}
          idleLabel="Submit"
          confirmedLabel="Sent"
          explorerUrl={props.explorerUrl}
        />
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

    // no explicit gas limit: let estimation decide - gas-repriced devnets
    // (glamsterdam) need far more than the classic costs
    tx.start(() => {
      if (props.localAccount) {
        // no connected wallet: sign & send locally with the mnemonic-derived account
        const walletClient = createWalletClient({
          account: props.localAccount,
          chain,
          transport: http(),
        });
        return walletClient.sendTransaction({
          to: props.builderDepositContract as `0x${string}`,
          value: totalValue,
          data,
        });
      }
      return submitRequest.sendTransactionAsync({
        to: props.builderDepositContract as `0x${string}`,
        account: address,
        chainId: chain?.id,
        value: totalValue,
        data,
      });
    });
  }
};

export default BuilderDepositEntry;
