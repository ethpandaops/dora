import { IValidator } from '../SubmitConsolidationsForm/SubmitConsolidationsFormProps';

export interface ISubmitBuilderDepositsFormProps {
  builderDepositContract: string;
  genesisForkVersion: string;
  explorerUrl?: string;
  faucetEnabled?: boolean;
  faucetAmount?: number;
  showGenerator?: boolean;
  // builder topup tab: builder rows in the validator-selector shape
  loadBuilders?: (address: string) => Promise<IValidator[]>;
  searchBuilders?: (searchTerm: string) => Promise<IValidator[]>;
}
