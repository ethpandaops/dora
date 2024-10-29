
export interface IWagmiChainConfig {
  chainId: number;
  name: string;
  rpcUrl: string;
  tokenName: string;
  tokenSymbol: string;
  explorerLink?: string;
}

export interface IWagmiComponentConfig {
    projectId: string;
    chains: IWagmiChainConfig[];
}
