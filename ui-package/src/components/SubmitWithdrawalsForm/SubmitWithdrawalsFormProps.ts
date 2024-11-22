
export interface ISubmitWithdrawalsFormProps {
  withdrawalContract: string;
  explorerUrl: string;
  minValidatorBalance: number;
  loadValidatorsCallback: (address: string) => Promise<IValidator[]>;
}

export interface IValidator {
  index: number;
  pubkey: string;
  credtype: string;
  balance: number;
  status: string;
}
