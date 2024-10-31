
export interface ISubmitDepositsFormProps {
  chainId: number;
  name: string;
  rpcUrl: string;
  tokenName: string;
  tokenSymbol: string;
  explorerLink?: string;
  genesisForkVersion: string;
  depositContract: string;
}
  