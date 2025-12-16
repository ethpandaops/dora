import React, { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import Select, { createFilter, OptionProps } from 'react-select'
import { IValidator } from './SubmitConsolidationsFormProps';
import { FilterOptionOption } from 'react-select/dist/declarations/src/filters';

interface IValidatorSelectorProps {
  placeholder: string;
  validators: IValidator[];
  onChange: (validator: IValidator) => void;
  value: IValidator | null;
  isLazyLoaded?: boolean;
  searchValidatorsCallback?: (searchTerm: string) => Promise<IValidator[]>;
}

const ValidatorSelector = (props: IValidatorSelectorProps): React.ReactElement => {
  const [inputValue, setInputValue] = useState<string>('');
  const [isLoading, setIsLoading] = useState<boolean>(false);
  const [searchResults, setSearchResults] = useState<IValidator[]>([]);
  const [options, setOptions] = useState<IValidator[]>(props.validators);
  const [isSearching, setIsSearching] = useState<boolean>(false);

  useEffect(() => {
    setOptions(props.validators);
  }, [props.validators]);

  const debounceTimerRef = useRef<NodeJS.Timeout | null>(null);
  
  const handleInputChange = useCallback((newValue: string) => {
    setInputValue(newValue);
    
    // Clear existing debounce timer
    if (debounceTimerRef.current) {
      clearTimeout(debounceTimerRef.current);
    }
    
    if (props.isLazyLoaded && props.searchValidatorsCallback) {
      const searchTerm = newValue.trim();
      
      if (searchTerm.length > 0) {
        // Only show loading after a small delay to avoid flickering
        const loadingTimer = setTimeout(() => {
          setIsLoading(true);
        }, 100);
        
        // Debounce the search request
        debounceTimerRef.current = setTimeout(() => {
          clearTimeout(loadingTimer);
          setIsLoading(true);
          setIsSearching(true);
          props.searchValidatorsCallback(searchTerm)
            .then(results => {
              setSearchResults(results);
              setOptions(results);
              setIsLoading(false);
            })
            .catch(() => {
              setIsLoading(false);
            });
        }, 300); // 300ms debounce delay
      } else {
        // Reset to original validators list when search is cleared
        setIsSearching(false);
        setOptions(props.validators);
      }
    }
  }, [props.isLazyLoaded, props.searchValidatorsCallback, props.validators]);
  
  // Cleanup debounce timer on unmount
  useEffect(() => {
    return () => {
      if (debounceTimerRef.current) {
        clearTimeout(debounceTimerRef.current);
      }
    };
  }, []);

  const filterOptions = useCallback((option: FilterOptionOption<IValidator>, inputValue: string) => {
    if (props.isLazyLoaded && isSearching) {
      return true; // Server-side filtering when actively searching
    }
    
    inputValue = inputValue.trim();
    if (inputValue) {
      if(inputValue.startsWith("0x") || !/^[0-9]+$/.test(inputValue)) {
        return option.data.pubkey.toLowerCase().includes(inputValue.toLowerCase());
      } else {
        return option.data.index.toString().startsWith(inputValue);
      }
    }
    return true;
  }, [props.isLazyLoaded, isSearching]);

  return (
    <div>
      {props.isLazyLoaded && (
        <div className="search-info mb-1 text-muted small">
          <span>
            {isSearching 
              ? "Showing search results. " 
              : "Showing your validators. "}
            {inputValue.trim() 
              ? inputValue.length < 3 
                ? "Type at least 3 characters to search external validators." 
                : "" 
              : "Type to search for any validator by index or pubkey."}
          </span>
        </div>
      )}
      <Select<IValidator, false>
        className="validator-selector"
        options={options}
        placeholder={props.placeholder}
        components={{
          Option: ({ children, ...props }) => (
            <ValidatorOption {...props}>
              {children}
            </ValidatorOption>
          )
        }}
        onChange={props.onChange}
        filterOption={filterOptions}
        isMulti={false}
        getOptionValue={(o) => o.pubkey}
        getOptionLabel={(o) => "Selected validator: [" + o.index + "] " + o.pubkey}
        value={props.value}
        onInputChange={handleInputChange}
        isLoading={isLoading}
        classNames={{
          control: () => "validator-selector-control",
          container: () => "validator-selector-container",
          menu: () => "validator-selector-menu",
          option: () => "validator-selector-option",
          singleValue: () => "validator-selector-single-value",
          input: () => "validator-selector-input"
        }}
      />
    </div>
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
        <span className="validator-balance">{formatBalance(data.balance, "{{ tokenSymbol }}")}</span>
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
