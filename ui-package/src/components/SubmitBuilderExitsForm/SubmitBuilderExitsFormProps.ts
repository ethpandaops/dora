export interface ISubmitBuilderExitsFormProps {
  builderExitContract: string;
  explorerUrl: string;
  loadBuildersCallback: (address: string) => Promise<IBuilder[]>;
}

export interface IBuilder {
  index: number;
  pubkey: string;
  executionAddress: string;
  status: string;
}
