import React, { useEffect, useState } from 'react';
import { isAddress } from 'viem';

export interface IEnsResolutionState {
  resolving: boolean;
  address: string | null; // resolved address when the input is an ENS name
  name: string | null;    // normalized name that resolved
  network: string | null;
  error: string | null;
}

export const emptyEnsState: IEnsResolutionState = {
  resolving: false,
  address: null,
  name: null,
  network: null,
  error: null,
};

interface IEnsAddressInputProps {
  value: string;
  placeholder?: string;
  small?: boolean;
  onChange(value: string): void;
  // reports resolution progress/results so parents can validate & use the address
  onEnsState?(state: IEnsResolutionState): void;
}

// Address input that also accepts ENS names, resolving them (debounced) via
// dora's /ens/resolve endpoint. Renders its own validation & resolution status.
const EnsAddressInput = (props: IEnsAddressInputProps): React.ReactElement => {
  const [ensState, setEnsState] = useState<IEnsResolutionState>(emptyEnsState);
  const trimmed = props.value.trim();
  const isEnsName = trimmed.includes('.') && !isAddress(trimmed);

  const { onEnsState } = props;
  const updateEnsState = (state: IEnsResolutionState) => {
    setEnsState(state);
    onEnsState?.(state);
  };

  useEffect(() => {
    if (!isEnsName) {
      updateEnsState(emptyEnsState);
      return;
    }

    const name = trimmed.toLowerCase();
    let cancelled = false;
    updateEnsState({ ...emptyEnsState, resolving: true });

    const timer = setTimeout(() => {
      fetch(`/ens/resolve?name=${encodeURIComponent(name)}`)
        .then((res) => res.json())
        .then((data) => {
          if (cancelled) return;
          if (data.matches && data.matches.length > 0) {
            updateEnsState({
              resolving: false,
              address: data.matches[0].address,
              name,
              network: data.matches[0].network ?? null,
              error: null,
            });
          } else {
            updateEnsState({ ...emptyEnsState, error: `Could not resolve ${name}` });
          }
        })
        .catch(() => {
          if (!cancelled) updateEnsState({ ...emptyEnsState, error: 'Name resolution failed' });
        });
    }, 500);

    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [trimmed, isEnsName]);

  const isValid = isAddress(trimmed) || (isEnsName && !ensState.resolving && !!ensState.address);
  const isInvalid = trimmed !== '' && !ensState.resolving && !isValid;

  return (
    <div className="flex-grow-1">
      <input
        type="text"
        className={`form-control ${props.small ? 'form-control-sm ' : ''}font-monospace ${
          isInvalid ? 'is-invalid' : isValid ? 'is-valid' : ''
        }`}
        placeholder={props.placeholder ?? '0x... or name.eth'}
        value={props.value}
        onChange={(e) => props.onChange(e.target.value)}
        style={props.small ? { minWidth: '120px' } : undefined}
      />
      {isEnsName && (
        <div className="form-text">
          {ensState.resolving && (
            <span>
              <span className="spinner-border spinner-border-sm me-1"></span>
              Resolving {trimmed}...
            </span>
          )}
          {!ensState.resolving && ensState.address && (
            <span className="text-success">
              <i className="fa fa-check me-1"></i>
              {ensState.name} → <span className="font-monospace">{ensState.address}</span>
              {ensState.network ? ` (${ensState.network})` : ''}
            </span>
          )}
          {!ensState.resolving && ensState.error && (
            <span className="text-danger">
              <i className="fa fa-times-circle me-1"></i>
              {ensState.error}
            </span>
          )}
        </div>
      )}
      {isInvalid && !isEnsName && (
        <div className="invalid-feedback">Please enter a valid Ethereum address or ENS name</div>
      )}
    </div>
  );
};

export default EnsAddressInput;
