import React from 'react';

export interface IWagmiChainConfig {
  chainId: number;
  name: string;
  rpcUrl: string;
  tokenName: string;
  tokenSymbol: string;
  explorerLink?: string;
}

export interface IWagmiRainbowProviderConfig {
    projectId: string;
    chains: IWagmiChainConfig[];
}

export interface IWagmiRainbowProviderProps extends IWagmiRainbowProviderConfig {
  children: React.ReactNode;
}
