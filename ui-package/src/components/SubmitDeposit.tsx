import React from 'react';
import { ConnectButton } from '@rainbow-me/rainbowkit';

import { ISubmitDepositProps } from './SubmitDepositProps';

import '@rainbow-me/rainbowkit/styles.css';
import './SubmitDeposit.css';

export interface ISubmitDepositState {
}

export default class SubmitDeposit extends React.PureComponent<ISubmitDepositProps, ISubmitDepositState> {
  constructor(props: ISubmitDepositProps, state: ISubmitDepositState) {
    super(props);

    this.state = {
    };
  }

  public render(): React.ReactElement<ISubmitDepositProps> {
    return (
      <div>
        Hello World
        <ConnectButton />
      </div>
    );
  }
}
