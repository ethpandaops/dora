import { IValidator } from '../SubmitConsolidationsForm/SubmitConsolidationsFormProps';

export interface ISubmitDepositsFormProps {
  chainId: number;
  name: string;
  rpcUrl: string;
  tokenName: string;
  tokenSymbol: string;
  explorerLink?: string;
  genesisForkVersion: string;
  depositContract: string;
  maxEffectiveBalance: string;
  maxEffectiveBalanceElectra: string;
  loadDepositTxs(pubkeys: string[]): Promise<{deposits: IDepositTx[], count: number, havemore: boolean}>;
  // Properties for topup deposit functionality
  loadValidators?: (address: string) => Promise<IValidator[]>;
  searchValidators?: (searchTerm: string) => Promise<IValidator[]>;
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
