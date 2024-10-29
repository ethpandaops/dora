
import React from 'react';
import ReactDOM from 'react-dom/client';
import { IWagmiComponentConfig } from './components/WagmiComponentProps';
import { ISubmitDepositProps } from './components/SubmitDepositProps';
import { IWagmiComponentProps } from './components/WagmiComponent';

export interface IComponentExports {
  [component: string]: (container: HTMLElement, cfg: any) => IComponentControls
}

export interface IComponentControls {
  unmount(): void
}

function exportComponents(uiPackages: IComponentExports) {
  // provider components
  const WagmiComponent = React.lazy<React.ComponentType<IWagmiComponentProps>>(() => import(/* webpackChunkName: "wagmi-component" */ './components/WagmiComponent'));
  
  // SubmitDeposit component
  const SubmitDeposit = React.lazy<React.ComponentType<ISubmitDepositProps>>(() => import(/* webpackChunkName: "submit-deposit" */ './components/SubmitDeposit'));
  uiPackages.SubmitDeposit = buildComponentLoader<{wagmiConfig: IWagmiComponentConfig, submitDepositConfig: ISubmitDepositProps}>(
    (config) => {
      return (
        <WagmiComponent {...config.wagmiConfig}>
          <SubmitDeposit {...config.submitDepositConfig} />
        </WagmiComponent>
      )
    }
  );
}

function buildComponentLoader<TCfg>(loader: (wagmiConfig: TCfg) => React.ReactNode): (container: HTMLElement, cfg: TCfg) => IComponentControls {
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

