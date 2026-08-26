import React, { useEffect, useState } from 'react';
import { ContainerType, ByteVectorType, UintNumberType, ValueOf } from "@chainsafe/ssz";
import bls from "@chainsafe/bls/herumi";
import { useAccount, useConfig } from 'wagmi';
import type { HDAccount } from 'viem';

import { IDeposit } from '../SubmitDepositsForm/DepositsTable';
import { useQueueDataCache } from '../../hooks/useQueueDataCache';
import { toReadableAmount } from '../../utils/ReadableAmount';
import BuilderDepositEntry from './BuilderDepositEntry';
import BatchSubmitButton from '../SubmitShared/BatchSubmitButton';
import { computeBuilderFees, getRequiredFee } from '../SubmitShared/builderDepositFee';

interface IBuilderDepositsTableProps {
  file?: File | null;
  deposits?: IDeposit[] | null;
  genesisForkVersion: string;
  builderDepositContract: string;
  explorerUrl?: string;
  // Submit transactions locally from this account instead of the connected wallet.
  localAccount?: HDAccount;
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

const SigningData = new ContainerType({
  object_root: new ByteVectorType(32),
  domain: new ByteVectorType(32),
});
type SigningData = ValueOf<typeof SigningData>;

const GWEI = 1000000000n;

const BuilderDepositsTable = (props: IBuilderDepositsTableProps): React.ReactElement => {
  const { chain: connectedChain } = useAccount();
  const wagmiConfig = useConfig();
  // without a connected wallet, fall back to the configured chain (local account mode)
  const chain = connectedChain ?? wagmiConfig.chains[0];
  const [deposits, setDeposits] = useState<IDeposit[] | null>(null);
  const [parseError, setParseError] = useState<string | null>(null);
  const [blsReady, setBlsReady] = useState(false);
  const [addExtraFee, setAddExtraFee] = useState(true);

  const { queueData, logData: cachedLogData } = useQueueDataCache(props.builderDepositContract, chain?.id);

  useEffect(() => {
    import('@chainsafe/bls/herumi').then((m) => m.init().then(() => setBlsReady(true)));
  }, []);

  const dataSource = props.deposits ? 'generated' : props.file ? 'file' : null;
  const dataSourceKey = props.deposits ? 'generated' : props.file?.name;

  useEffect(() => {
    if (!dataSource || !blsReady) return;
    parseDeposits().then((res) => {
      setDeposits(res);
      setParseError(null);
    }).catch((error) => {
      setParseError(error.message);
      setDeposits(null);
    });
  }, [dataSourceKey, blsReady]);

  // Compute the predeploy queue fee (shared across all rows).
  const { isPreFork, queueLength, requestFee, batchFeeBase } = computeBuilderFees(queueData, cachedLogData, addExtraFee);

  const validDeposits = (deposits || []).filter((d) => d.validity);

  let feeFactor = 0;
  let feeUnit = "Wei";
  if (requestFee > 100000000000000n) { feeFactor = 18; feeUnit = "ETH"; }
  else if (requestFee > 100000n) { feeFactor = 9; feeUnit = "Gwei"; }

  return (
    <div>
      <div className="row mt-3">
        <div className="col-12"><b>Step 3: Review and submit builder deposits</b></div>
      </div>
      <div className="row mt-2">
        <div className="col-lg-2 col-sm-3 font-weight-bold">Deposit Source:</div>
        <div className="col-lg-10 col-sm-9">
          {props.file ? props.file.name : <span className="text-warning"><i className="fa fa-magic me-1"></i>Generated (devnet only)</span>}
          {!props.file && props.localAccount && (
            <span className="ms-2 font-monospace">from {props.localAccount.address}</span>
          )}
        </div>
      </div>

      {parseError ?
        <div className="alert alert-danger">The provided file is not a valid builder deposit data file! ({parseError})</div>
      : isPreFork ?
        <div className="alert alert-danger mt-2">The network is not on Gloas yet, so builder deposit requests can not be submitted.</div>
      : <>
          <div className="row">
            <div className="col-lg-2 col-sm-3 font-weight-bold">Builder Deposit Contract:</div>
            <div className="col-lg-10 col-sm-9">{props.builderDepositContract}</div>
          </div>
          <div className="row">
            <div className="col-lg-2 col-sm-3 font-weight-bold">Deposit Queue:</div>
            <div className="col-lg-10 col-sm-9">{queueLength.toString()} Deposits</div>
          </div>
          <div className="row">
            <div className="col-lg-2 col-sm-3 font-weight-bold">Queue fee per deposit:</div>
            <div className="col-lg-10 col-sm-9">
              {toReadableAmount(requestFee, feeFactor, feeUnit, 6)}
              <span className="ms-2">
                <input type="checkbox" className="form-check-input" id="addExtraFee" checked={addExtraFee} onChange={(e) => setAddExtraFee(e.target.checked)} />
                <label htmlFor="addExtraFee" className="ms-1">add extra fee</label>
              </span>
            </div>
          </div>

          {validDeposits.length > 1 && (
            <BatchSubmitButton
              target={props.builderDepositContract}
              count={validDeposits.length}
              buildBatch={buildBatch}
              localAccount={props.localAccount}
              explorerUrl={props.explorerUrl}
            />
          )}

          {!deposits ? <p>Loading...</p> : deposits.length === 0 ? <p>No deposits found</p> : (
            <div className="table-ellipsis mt-1">
              <table className="table" style={{ width: "100%" }}>
                <thead>
                  <tr>
                    <th>Pubkey</th>
                    <th style={{ minWidth: "430px", maxWidth: "460px" }}>Withdrawal Credentials</th>
                    <th style={{ width: "150px" }}>Amount</th>
                    <th style={{ width: "80px" }}>Validity</th>
                    <th style={{ width: "120px" }}>Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {deposits.map((deposit: IDeposit) => (
                    <BuilderDepositEntry
                      key={deposit.pubkey}
                      deposit={deposit}
                      builderDepositContract={props.builderDepositContract}
                      requestFee={requestFee}
                      explorerUrl={props.explorerUrl}
                      localAccount={props.localAccount}
                    />
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </>
      }
    </div>
  );

  // Per-item batch payloads: raw 184-byte calldata per deposit, with per-position
  // escalating fees (the predeploy raises the fee for every request beyond
  // TARGET_PER_BLOCK within the same block).
  function buildBatch(): { calls: `0x${string}`[]; values: bigint[] } {
    const calls = validDeposits.map((deposit) => {
      const pubkey = deposit.pubkey.replace(/^0x/, "");
      const wdCreds = deposit.withdrawal_credentials.replace(/^0x/, "");
      const signature = deposit.signature.replace(/^0x/, "");
      const amountHex = deposit.amount.toString(16).padStart(16, "0");
      return ("0x" + pubkey + wdCreds + amountHex + signature) as `0x${string}`;
    });
    const values = validDeposits.map((deposit, i) =>
      BigInt(deposit.amount) * GWEI + getRequiredFee(batchFeeBase + BigInt(i))
    );
    return { calls, values };
  }

  async function parseDeposits(): Promise<IDeposit[]> {
    let json: IDeposit[];
    if (props.deposits) {
      json = props.deposits;
    } else if (props.file) {
      json = JSON.parse(await props.file.text());
    } else {
      throw new Error("No deposit data provided");
    }

    // builder-deposit signing domain: DOMAIN_BUILDER_DEPOSIT = 0x0E000000
    const forkData = ForkData.fromJson({
      current_version: props.genesisForkVersion,
      genesis_validators_root: "0x0000000000000000000000000000000000000000000000000000000000000000",
    });
    const forkDataRoot = ForkData.hashTreeRoot(forkData);
    const signingDomain = new Uint8Array(32);
    signingDomain.set([0x0e, 0x00, 0x00, 0x00]);
    signingDomain.set(forkDataRoot.slice(0, 28), 4);

    return json.map((deposit: IDeposit) => {
      const credsOk = deposit.withdrawal_credentials.replace(/^0x/, "").substring(0, 2).toLowerCase() === "b0";
      deposit.validity = props.deposits ? credsOk : (credsOk && verifyDeposit(deposit, signingDomain));
      return deposit;
    });
  }

  function verifyDeposit(deposit: IDeposit, signingDomain: Uint8Array): boolean {
    try {
      const depositMessage: DepositMessage = DepositMessage.fromJson({
        pubkey: deposit.pubkey,
        withdrawal_credentials: deposit.withdrawal_credentials,
        amount: deposit.amount,
      });
      const depositRoot = DepositMessage.hashTreeRoot(depositMessage);
      const signature = bls.Signature.fromHex(deposit.signature);
      const pubkey = bls.PublicKey.fromHex(deposit.pubkey);
      const signingData: SigningData = { object_root: depositRoot, domain: signingDomain };
      const signingDataRoot = SigningData.hashTreeRoot(signingData);
      return signature.verify(pubkey, signingDataRoot);
    } catch {
      return false;
    }
  }

};

export default BuilderDepositsTable;
