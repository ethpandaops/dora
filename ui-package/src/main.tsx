
import React from 'react';
import ReactDOM from 'react-dom/client';
import { IWagmiRainbowProviderProps, IWagmiRainbowProviderConfig } from './components/WagmiRainbowProvider/WagmiRainbowProviderProps';
import { ISubmitDepositsFormProps } from './components/SubmitDepositsForm/SubmitDepositsFormProps';
import { ISubmitConsolidationsFormProps } from './components/SubmitConsolidationsForm/SubmitConsolidationsFormProps';
import { ISubmitWithdrawalsFormProps } from './components/SubmitWithdrawalsForm/SubmitWithdrawalsFormProps';
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

  // SubmitConsolidationsForm component
  const SubmitConsolidationsForm = React.lazy<React.ComponentType<ISubmitConsolidationsFormProps>>(() => import(/* webpackChunkName: "submit-consolidation" */ './components/SubmitConsolidationsForm/SubmitConsolidationsForm'));
  uiPackages.SubmitConsolidationsForm = buildComponentLoader<{wagmiConfig: IWagmiRainbowProviderConfig, submitConsolidationsConfig: ISubmitConsolidationsFormProps}>(
    (config) => {
      return (
        <WagmiRainbowProvider {...config.wagmiConfig}>
          <SubmitConsolidationsForm {...config.submitConsolidationsConfig} />
        </WagmiRainbowProvider>
      )
    }
  );

  // SubmitWithdrawalsForm component
  const SubmitWithdrawalsForm = React.lazy<React.ComponentType<ISubmitWithdrawalsFormProps>>(() => import(/* webpackChunkName: "submit-withdrawal" */ './components/SubmitWithdrawalsForm/SubmitWithdrawalsForm'));
  uiPackages.SubmitWithdrawalsForm = buildComponentLoader<{wagmiConfig: IWagmiRainbowProviderConfig, submitWithdrawalsConfig: ISubmitWithdrawalsFormProps}>(
    (config) => {
      return (
        <WagmiRainbowProvider {...config.wagmiConfig}>
          <SubmitWithdrawalsForm {...config.submitWithdrawalsConfig} />
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

