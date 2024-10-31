
export interface ISubmitConsolidationsFormProps {
  consolidationContract: string;
  loadValidatorsCallback: (address: string) => Promise<IValidator[]>;
}

export interface IValidator {
  index: number;
  pubkey: string;
  credtype: string;
  balance: number;
  status: string;
}
