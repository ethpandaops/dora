import React, { useEffect } from 'react';
import { useState } from 'react';
import {ContainerType, ByteVectorType, UintNumberType, ValueOf} from "@chainsafe/ssz";
import bls from "@chainsafe/bls/herumi";

import DepositEntry from './DepositEntry';
import { IDepositTx } from './SubmitDepositsFormProps';

interface IDepositsTableProps {
  file: File;
  genesisForkVersion: string;
  depositContract: string;
  loadDepositTxs(pubkeys: string[]): Promise<{deposits: IDepositTx[], count: number, havemore: boolean}>;
}

export interface IDeposit {
  pubkey: string;
  withdrawal_credentials: string;
  amount: number; // in gwei
  signature: string;
  deposit_message_root: string;
  deposit_data_root: string;
  fork_version: string;
  network_name: string;
  deposit_cli_version: string;

  validity: boolean;
  depositTxs?: IDepositTx[];
}

export interface IDepositTxStats {
  count: number;
  havemore: boolean;
}

const DepositMessage = new ContainerType({
  pubkey: new ByteVectorType(48),
  withdrawal_credentials: new ByteVectorType(32),
  amount: new UintNumberType(8),
});
type DepositMessage = ValueOf<typeof DepositMessage>;


const ForkData = new ContainerType({
  current_version: new ByteVectorType(4),
  genesis_validators_root: new ByteVectorType(32),
});
type ForkData = ValueOf<typeof ForkData>;

const SigningData = new ContainerType({
  object_root: new ByteVectorType(32),
  domain: new ByteVectorType(32),
});
type SigningData = ValueOf<typeof SigningData>;


const DepositsTable = (props: IDepositsTableProps): React.ReactElement => {
  const [deposits, setDeposits] = useState<IDeposit[] | null>(null);
  const [parseError, setParseError] = useState<string | null>(null);
  const [loadDepositsError, setLoadDepositsError] = useState<string | null>(null);
  const [depositTxStats, setDepositTxStats] = useState<IDepositTxStats | null>(null);

  useEffect(() => {
    parseDeposits().then((res) => {
      setDeposits(res.deposits);
      setParseError(null);
      setLoadDepositsError(res.loadDepositsErr);
      setDepositTxStats(res.depositStats);
    }).catch((error) => {
      setParseError(error.message);
      setDeposits(null);
      setLoadDepositsError(null);
      setDepositTxStats(null);
    });
  }, [props.file]);

  return (
    <div>
      <div className="row mt-3">
        <div className="col-12">
          <b>Step 3: Review and submit deposits</b>
        </div>
      </div>
      <div className="row mt-2">
        <div className="col-lg-2 col-sm-3 font-weight-bold">Deposit Data File:</div>
        <div className="col-lg-10 col-sm-9">{props.file.name}</div>
      </div>
      {parseError ?
        <div className="alert alert-danger">The provided file is not a valid deposit data file! ({parseError})</div> :
        <>
          <div className="row">
            <div className="col-lg-2 col-sm-3 font-weight-bold">Deposit CLI Version:</div>
            <div className="col-lg-10 col-sm-9">{deposits?.[0]?.deposit_cli_version}</div>
          </div>
          <div className="row">
            <div className="col-lg-2 col-sm-3 font-weight-bold">Target Network:</div>
          <div className="col-lg-10 col-sm-9">
            {deposits?.[0]?.network_name}
            <span className="text-secondary-emphasis ms-2" style={{fontSize: "0.9em"}} data-bs-toggle="tooltip" data-bs-placement="top" title="Genesis Fork Version">(GFV: {deposits?.[0]?.fork_version})</span>
            </div>
          </div>

          {loadDepositsError ? 
            <div className="alert alert-danger mt-2">
              Failed to load deposit transactions: <br />
              {loadDepositsError}<br />
              Duplicate deposits may not be displayed correctly!
            </div> 
          : null}

          {depositTxStats && depositTxStats.count > 0 ? 
            <div className="alert alert-warning mt-2">
              We've found {depositTxStats.havemore ? "more than " : ""}{depositTxStats.count} deposit transactions matching your validator pubkeys. Double check each deposit to avoid double deposits.
            </div> 
          : null}

          {!deposits ? <p>Loading...</p> : deposits.length === 0 ? <p>No deposits found</p> : (
            <div className="table-ellipsis mt-1">
              <table className="table" style={{width: "100%"}}>
                <thead>
                  <tr>
                    <th>Pubkey</th>
                    <th style={{minWidth: "430px", maxWidth: "460px"}}>Withdrawal Credentials</th>
                    <th style={{width: "150px"}}>Amount</th>
                    <th style={{width: "80px"}}>Validity</th>
                    <th style={{width: "120px"}}>Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {deposits.map((deposit: IDeposit) => {
                    return <DepositEntry deposit={deposit} depositContract={props.depositContract} key={deposit.pubkey} />;
                  })}
                </tbody>
              </table>
            </div>
          )}
        </>
      }
    </div>
  );

  async function parseDeposits(): Promise<{deposits: IDeposit[], loadDepositsErr: string, depositStats: IDepositTxStats}> {
    try {
      const text = await props.file.text();
      const json: IDeposit[] = JSON.parse(text);

      let pubkeys = json.map((deposit: IDeposit) => deposit.pubkey);
      let depositTxs = [];
      let loadDepositsErr = null;
      let depositStats: IDepositTxStats = null;
      try {
        let depositsRes = await props.loadDepositTxs(pubkeys);
        depositTxs = depositsRes.deposits;
        depositStats = {
          count: depositsRes.count,
          havemore: depositsRes.havemore
        };
      } catch (error) {
        loadDepositsErr = error.toString();
      }

      // compute signing domain
      const forkData: ForkData = ForkData.fromJson({
        current_version: props.genesisForkVersion, // genesis fork version of the target network
        genesis_validators_root: "0x0000000000000000000000000000000000000000000000000000000000000000", // hardcoded zero hash for deposits as they are valid even before genesis
      });
      let forkDataRoot = ForkData.hashTreeRoot(forkData);
      let signingDomain = new Uint8Array(32);
      signingDomain.set([0x03, 0x00, 0x00, 0x00]);
      signingDomain.set(forkDataRoot.slice(0, 28), 4);
      
      return {
        deposits: json.map((deposit: IDeposit) => {
          deposit.validity = verifyDeposit(deposit, signingDomain);
          deposit.depositTxs = depositTxs.filter((tx: IDepositTx) => tx.pubkey === deposit.pubkey);
          return deposit;
        }),
        loadDepositsErr: loadDepositsErr,
        depositStats: depositStats
      };
    } catch (error) {
      console.error(error);
      throw error;
    }
  }

  function verifyDeposit(deposit: IDeposit, signingDomain: Uint8Array): boolean {
    let depositMessage: DepositMessage = DepositMessage.fromJson({
      pubkey: deposit.pubkey,
      withdrawal_credentials: deposit.withdrawal_credentials,
      amount: deposit.amount,
    });
    let depositRoot = DepositMessage.hashTreeRoot(depositMessage);
    
    let signature = bls.Signature.fromHex(deposit.signature);
    let pubkey = bls.PublicKey.fromHex(deposit.pubkey);

    let signingData: SigningData = {
      object_root: depositRoot,
      domain: signingDomain,
    };
    let signingDataRoot = SigningData.hashTreeRoot(signingData);

    return signature.verify(pubkey, signingDataRoot);
  }

}

export default DepositsTable;
