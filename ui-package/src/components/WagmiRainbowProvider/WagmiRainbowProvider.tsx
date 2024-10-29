import React from 'react';
import { getDefaultConfig, RainbowKitProvider } from '@rainbow-me/rainbowkit';
import { Config, WagmiProvider } from 'wagmi';
import { Chain } from 'wagmi/chains';
import { QueryClientProvider, QueryClient } from "@tanstack/react-query";
import { ChainFormatters, defineChain } from "viem";

import { IWagmiRainbowProviderProps } from './WagmiRainbowProviderProps';
import './WagmiRainbowProvider.scss';

export interface IWagmiRainbowProviderState {
  wagmiConfig: Config;
  queryClient: QueryClient;
}

export default class WagmiRainbowProvider extends React.PureComponent<IWagmiRainbowProviderProps, IWagmiRainbowProviderState> {
  constructor(props: IWagmiRainbowProviderProps, state: IWagmiRainbowProviderState) {
    super(props);

    let chains = props.chains.map((chain) => {
      let chainOpts: Chain<ChainFormatters> = {
        id: chain.chainId,
        name: chain.name,
        nativeCurrency: { name: chain.tokenName, symbol: chain.tokenSymbol, decimals: 18 },
        rpcUrls: {
          default: {
            http: [chain.rpcUrl],
          },
        },
      }

      if (chain.explorerLink) {
        chainOpts.blockExplorers = {
          default: { name: "Explorer", url: chain.explorerLink },
        };
      }

      return defineChain(chainOpts);
    });
    
    let wagmiConfig = getDefaultConfig({
      appName: 'Dora',
      projectId: props.projectId,
      chains: chains as any,
      ssr: false,
    });

    let queryClient = new QueryClient();

    this.state = {
      wagmiConfig: wagmiConfig,
      queryClient: queryClient,
    };
  }

  public render(): React.ReactElement<IWagmiRainbowProviderProps> {
    return (
      <WagmiProvider config={this.state.wagmiConfig}>
        <QueryClientProvider client={this.state.queryClient}>
          <RainbowKitProvider>
            {this.props.children}
          </RainbowKitProvider>
        </QueryClientProvider>
      </WagmiProvider>
    );
  }
}

