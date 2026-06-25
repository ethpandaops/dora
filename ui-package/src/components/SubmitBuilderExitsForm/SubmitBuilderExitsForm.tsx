import React, { useEffect, useState } from 'react';
import { ConnectButton } from '@rainbow-me/rainbowkit';
import { useAccount } from 'wagmi';

import { ISubmitBuilderExitsFormProps, IBuilder } from './SubmitBuilderExitsFormProps';
import BuilderExitReview from './BuilderExitReview';

const PUBKEY_RE = /^0x[0-9a-fA-F]{96}$/;

const SubmitBuilderExitsForm = (props: ISubmitBuilderExitsFormProps): React.ReactElement => {
  const { address: walletAddress, isConnected, chain } = useAccount();
  const [builders, setBuilders] = useState<IBuilder[] | null>(null);
  const [loadingError, setLoadingError] = useState<string | null>(null);
  const [manualMode, setManualMode] = useState(false);
  const [selectedPubkey, setSelectedPubkey] = useState<string>('');
  const [manualPubkey, setManualPubkey] = useState<string>('');

  useEffect(() => {
    if (walletAddress) {
      setBuilders(null);
      setLoadingError(null);
      props.loadBuildersCallback(walletAddress).then(setBuilders).catch((e) => setLoadingError(String(e)));
    } else {
      setBuilders(null);
    }
  }, [walletAddress, props.loadBuildersCallback]);

  useEffect(() => {
    const timeoutId = window.setTimeout(() => {
      (window as any).explorer?.initControls?.();
    }, 100);
    return () => window.clearTimeout(timeoutId);
  }, []);

  const pubkey = manualMode ? manualPubkey.trim() : selectedPubkey;
  const pubkeyValid = PUBKEY_RE.test(pubkey);

  return (
    <div className="submit-deposits">
      <div className="row">
        <div className="col-12">
          <h3>Submit builder exit requests</h3>
          <p>This tool creates an exit request for a builder you own. The connected wallet must be the builder's execution (withdrawal) address.</p>
        </div>
      </div>

      <div className="row mt-3">
        <div className="col-12"><b>Step 1: Connect your wallet</b></div>
      </div>
      <div className="row">
        <div className="col-12 p-2">
          <ConnectButton showBalance={true} accountStatus={{ smallScreen: 'avatar', largeScreen: 'full' }} chainStatus={{ smallScreen: 'icon', largeScreen: 'full' }} />
        </div>
      </div>

      {isConnected && chain ?
        <>
          <div className="row mt-3">
            <div className="col-12">
              <label className="form-label"><b>Step 2: Select builder</b></label>
            </div>
            <div className="col-12">
              <div className="form-text">
                Select one of the builders owned by your address, or enter a builder public key manually.
              </div>
            </div>
          </div>

          {loadingError ?
            <div className="alert alert-warning">
              <i className="fa fa-exclamation-triangle me-2"></i>
              Could not load builders for your address: {loadingError}. You can still enter a pubkey manually below.
            </div>
          : builders == null ?
            <div>Please wait while we load your builders...</div>
          : null}

          {!manualMode && builders && builders.length > 0 ?
            <div className="row">
              <div className="col-12 col-lg-11">
                <select className="form-select" value={selectedPubkey} onChange={(e) => setSelectedPubkey(e.target.value)}>
                  <option value="">-- select a builder --</option>
                  {builders.map((b) => (
                    <option key={b.pubkey} value={b.pubkey}>
                      Builder {b.index} ({b.status}) - {b.pubkey.substring(0, 18)}...
                    </option>
                  ))}
                </select>
              </div>
            </div>
          : null}

          {!manualMode && builders && builders.length === 0 && !loadingError ?
            <div className="alert alert-info">No active builders found for your address. You can enter a pubkey manually below.</div>
          : null}

          <div className="row mt-2">
            <div className="col-12">
              <div className="form-check">
                <input className="form-check-input" type="checkbox" id="manualMode" checked={manualMode} onChange={(e) => setManualMode(e.target.checked)} />
                <label className="form-check-label" htmlFor="manualMode">Enter builder public key manually</label>
              </div>
            </div>
          </div>

          {manualMode ?
            <div className="row mt-1">
              <div className="col-12 col-lg-11">
                <input
                  type="text"
                  className={"form-control" + (manualPubkey && !pubkeyValid ? " is-invalid" : "")}
                  placeholder="0x... (48-byte builder public key)"
                  value={manualPubkey}
                  onChange={(e) => setManualPubkey(e.target.value)}
                />
                {manualPubkey && !pubkeyValid ?
                  <div className="invalid-feedback">Enter a valid 48-byte (96 hex char) public key.</div>
                : null}
              </div>
            </div>
          : null}

          {pubkeyValid ?
            <>
              <div className="row mt-3">
                <div className="col-12">
                  <label className="form-label"><b>Step 3: Review & submit exit request</b></label>
                </div>
              </div>
              <BuilderExitReview
                key={pubkey}
                pubkey={pubkey}
                builderExitContract={props.builderExitContract}
                explorerUrl={props.explorerUrl}
              />
            </>
          : null}
        </>
      : null}
    </div>
  );
};

export default SubmitBuilderExitsForm;
