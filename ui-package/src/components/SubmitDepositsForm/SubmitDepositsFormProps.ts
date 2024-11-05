
export interface ISubmitDepositsFormProps {
  chainId: number;
  name: string;
  rpcUrl: string;
  tokenName: string;
  tokenSymbol: string;
  explorerLink?: string;
  genesisForkVersion: string;
  depositContract: string;
  loadDepositTxs(pubkeys: string[]): Promise<{deposits: IDepositTx[], count: number, havemore: boolean}>;
}

export interface IDepositTx {
  pubkey: string;
  amount: number;
  block: number;
  block_hash: string;
  block_time: number;
  tx_origin: string;
  tx_target: string;
  tx_hash: string;
}
