
import React from 'react';
import ReactDOM from 'react-dom/client';
import { IWagmiRainbowProviderProps, IWagmiRainbowProviderConfig } from './components/WagmiRainbowProvider/WagmiRainbowProviderProps';
import { ISubmitDepositsFormProps } from './components/SubmitDepositsForm/SubmitDepositsFormProps';

export interface IComponentExports {
  [component: string]: (container: HTMLElement, cfg: any) => IComponentControls
}

export interface IComponentControls {
  unmount(): void
}

function exportComponents(uiPackages: IComponentExports) {
  // provider components
  const WagmiRainbowProvider = React.lazy<React.ComponentType<IWagmiRainbowProviderProps>>(() => import(/* webpackChunkName: "wagmi-component" */ './components/WagmiRainbowProvider/WagmiRainbowProvider'));
  
  // SubmitDepositsForm component
  const SubmitDepositsForm = React.lazy<React.ComponentType<ISubmitDepositsFormProps>>(() => import(/* webpackChunkName: "submit-deposit" */ './components/SubmitDepositsForm/SubmitDepositsForm'));
  uiPackages.SubmitDepositsForm = buildComponentLoader<{wagmiConfig: IWagmiRainbowProviderConfig, submitDepositConfig: ISubmitDepositsFormProps}>(
    (config) => {
      return (
        <WagmiRainbowProvider {...config.wagmiConfig}>
          <SubmitDepositsForm {...config.submitDepositConfig} />
        </WagmiRainbowProvider>
      )
    }
  );
}

function buildComponentLoader<TCfg>(loader: (cfg: TCfg) => React.ReactNode) {
  return (container: HTMLElement, cfg: TCfg) => {
    const root = ReactDOM.createRoot(container);
    root.render(
      <React.Suspense fallback={<div>Loading...</div>}>
        {loader(cfg)}
      </React.Suspense>
    );

    return {
      unmount: () => root.unmount(),
    }
  }
}

(() => {
  globalThis.doraUiComponents = globalThis.doraUiComponents || {};
  exportComponents(globalThis.doraUiComponents);
})()

