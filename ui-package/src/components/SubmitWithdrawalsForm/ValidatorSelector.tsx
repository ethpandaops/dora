import React from 'react';
import Select, { createFilter, OptionProps } from 'react-select'
import { IValidator } from './SubmitWithdrawalsFormProps';
import { FilterOptionOption } from 'react-select/dist/declarations/src/filters';

interface IValidatorSelectorProps {
  validators: IValidator[];
  onChange: (validator: IValidator) => void;
  value: IValidator | null;
}

const ValidatorSelector = (props: IValidatorSelectorProps): React.ReactElement => {
  const filterOptions = (option: FilterOptionOption<IValidator>, inputValue: string) => {
    inputValue = inputValue.trim();
    if (inputValue) {
      if(inputValue.startsWith("0x") || !/^[0-9]+$/.test(inputValue)) {
        return option.data.pubkey.toLowerCase().includes(inputValue.toLowerCase());
      } else {
        return option.data.index.toString().startsWith(inputValue);
      }
    }
    return true;
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
      filterOption={filterOptions}
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
  const { data, innerRef, innerProps, isSelected, isFocused } = props;
  
  let classNames = ["validator-selector-option"];
  if (isSelected) {
    classNames.push("selected");
  }
  if (isFocused) {
    classNames.push("focused");
  }

  return (
    <span {...innerProps} className={classNames.join(" ")} ref={innerRef}>
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
