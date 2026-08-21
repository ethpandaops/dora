import React from 'react';
import { ConnectButton } from '@rainbow-me/rainbowkit';

interface IDepositSourcePanelsProps {
  // hide the generator column entirely (e.g. non-devnet deployments)
  showGenerator: boolean;
  fileLabel: string;
  fileHint: React.ReactNode;
  generatorTitle: string;
  generatorHint: React.ReactNode;
  // extra controls next to the connect button (e.g. a faucet button)
  walletExtra?: React.ReactNode;
  onFileSelected(file: File): void;
  onGenerateClick(): void;
}

// Two-column deposit source chooser used by the submit deposit pages: connect a
// wallet & upload a deposit data file on the left, or generate deposits in the
// browser (devnet only, no wallet needed) on the right. With the generator
// disabled the wallet column spans the full width.
const DepositSourcePanels = (props: IDepositSourcePanelsProps): React.ReactElement => {
  return (
    <div className="row mt-3 g-3 align-items-stretch">
      <div className={props.showGenerator ? 'col-lg-6' : 'col-12'}>
        <div className="card h-100">
          <div className="card-body">
            <h5 className="card-title">
              <i className="fa fa-wallet me-2"></i>
              Use a wallet &amp; deposit file
            </h5>
            <div className="mt-3"><b>Step 1: Connect your wallet</b></div>
            <div className="py-2 d-flex align-items-center gap-3 flex-wrap">
              <ConnectButton showBalance={true} accountStatus={{ smallScreen: 'avatar', largeScreen: 'full' }} chainStatus={{ smallScreen: 'icon', largeScreen: 'full' }} />
              {props.walletExtra}
            </div>
            <div className="mt-2"><b>{props.fileLabel}</b></div>
            <input
              type="file"
              className="form-control mt-2"
              onChange={(e: React.ChangeEvent<HTMLInputElement>) => {
                if (e.target.files && e.target.files.length > 0) {
                  props.onFileSelected(e.target.files[0]);
                }
              }}
            />
            <p className="text-secondary-emphasis mt-2 mb-0">{props.fileHint}</p>
          </div>
        </div>
      </div>
      {props.showGenerator && (
        <div className="col-lg-6">
          <div className="card h-100">
            <div className="card-body d-flex flex-column">
              <h5 className="card-title d-flex align-items-center justify-content-between">
                <span>
                  <i className="fa fa-magic me-2"></i>
                  {props.generatorTitle}
                </span>
                <span className="badge rounded-pill text-bg-warning">devnet only</span>
              </h5>
              <p className="text-secondary-emphasis mb-3">{props.generatorHint}</p>
              <ul className="list-unstyled mb-3">
                <li className="d-flex align-items-baseline mb-2">
                  <i className="fa fa-dice text-warning me-2"></i>
                  <span>Generate a random mnemonic in your browser</span>
                </li>
                <li className="d-flex align-items-baseline mb-2">
                  <i className="fa fa-tint text-warning me-2"></i>
                  <span>Request devnet funds for its wallet from the faucet</span>
                </li>
                <li className="d-flex align-items-baseline">
                  <i className="fa fa-paper-plane text-warning me-2"></i>
                  <span>Sign &amp; submit the deposits - no wallet extension needed</span>
                </li>
              </ul>
              <div className="mt-auto d-grid">
                <button className="btn btn-warning" onClick={() => props.onGenerateClick()}>
                  <i className="fa fa-magic me-1"></i>
                  Generate Deposits
                </button>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};

export default DepositSourcePanels;
