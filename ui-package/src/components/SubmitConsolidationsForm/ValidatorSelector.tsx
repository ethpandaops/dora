import React, { CSSProperties } from 'react';
import { useAccount, useWriteContract } from 'wagmi';
import { useState } from 'react';
import Select, { createFilter, OptionProps } from 'react-select'
import { IValidator } from './SubmitConsolidationsFormProps';
import { FilterOptionOption } from 'react-select/dist/declarations/src/filters';

interface IValidatorSelectorProps {
  validators: IValidator[];
  onChange: (validator: IValidator) => void;
  value: IValidator | null;
}

const ValidatorSelector = (props: IValidatorSelectorProps): React.ReactElement => {
  const filterConfig = {
    ignoreCase: true,
    ignoreAccents: true,
    trim: true,
    matchFrom: "any" as const,
    stringify: (option: FilterOptionOption<IValidator>) => option.data.index + ": " + option.data.pubkey
  };

  return (
    <Select<IValidator, false>
      className="validator-selector"
      options={props.validators}
      placeholder="Select a validator"
      components={{
        Option: ({ children, ...props }) => (
          <ValidatorOption {...props}>
            {children}
          </ValidatorOption>
        )
      }}
      onChange={(e) => {
        props.onChange(e);
      }}
      filterOption={createFilter<IValidator>(filterConfig)}
      isMulti={false}
      isOptionSelected={(o, v) => v.some((i) => i.index === o.index)}
      getOptionLabel={(o) => "Selected validator: [" + o.index + "] " + o.pubkey}
      getOptionValue={(o) => o.pubkey}
      value={props.value}
      classNames={{
        control: () => "validator-selector-control",
        container: () => "validator-selector-container",
        menu: () => "validator-selector-menu",
        option: () => "validator-selector-option",
        singleValue: () => "validator-selector-single-value",
        input: () => "validator-selector-input"
      }}
    />
  );

}

const ValidatorOption = (props: OptionProps<IValidator, false>) => {
  const { data, innerRef, innerProps, isSelected } = props;
  
  return (
    <span {...innerProps} className={isSelected ? "validator-selector-option selected" : "validator-selector-option"} ref={innerRef}>
      <span className="validator-item">
        <span className="validator-index">{data.index}</span>
        <span className="validator-pubkey">{data.pubkey}</span>
        <span className="validator-balance">{formatBalance(data.balance, "ETH")}</span>
        <span className="validator-status">{formatStatus(data.status)}</span>
      </span>
    </span>
  );

};

export function formatStatus(status: string) {
  switch (status.toLowerCase()) {
    case "active":
      return <span className="badge rounded-pill text-bg-success status-badge">{status}</span>;
    case "exited":
    case "exiting":
    case "slashed":
    case "pending":
      return <span className="badge rounded-pill text-bg-danger status-badge">{status}</span>;
    default:
      return <span className="badge rounded-pill text-bg-warning status-badge">{status}</span>;
  }
}

export function formatBalance(amount: number, ethSymbol: string) {
  let amountEth = amount / 1e9;
  return amountEth.toFixed(0) + " " + ethSymbol;
}

export default ValidatorSelector;
