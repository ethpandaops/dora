import React, { useState, useEffect, useMemo, useCallback } from 'react';
import { Modal } from 'react-bootstrap';
import { useBalance } from 'wagmi';
import { isAddress } from 'viem';
import { mnemonicToAccount } from 'viem/accounts';

import { IDeposit } from './DepositsTable';
import FaucetButton from './FaucetButton';
import { useEnsLookup } from '../SubmitShared/useEnsLookup';
import EnsAddressInput, { IEnsResolutionState, emptyEnsState } from '../SubmitShared/EnsAddressInput';
import { toReadableAmount } from '../../utils/ReadableAmount';
import {
  validateMnemonicWords,
  validateWithdrawalCredentials,
  generateDeposits,
  generateRandomMnemonic,
  ethToGwei,
  GeneratorConfig,
  ValidatorOverride,
  CredentialType,
  WithdrawalCredentialConfig,
  DepositDomainType,
} from './DepositGenerator';

interface IDepositGeneratorModalProps {
  genesisForkVersion: string;
  defaultWithdrawalAddress?: string;
  onClose: () => void;
  // mnemonic is the normalized phrase the deposits were derived from; callers can use
  // it to derive the EL wallet (m/44'/60'/0'/0/0) and submit without a browser wallet.
  onGenerate: (deposits: IDeposit[], mnemonic: string) => void;
  // Builder mode (Gloas/EIP-8282): sign under DOMAIN_BUILDER_DEPOSIT and lock the
  // withdrawal credential to the 0xB0 builder prefix.
  domainType?: DepositDomainType;
  lockBuilderCredentials?: boolean;
  // Devnet faucet: offer funding the wallet derived from the mnemonic.
  faucetEnabled?: boolean;
  faucetAmount?: number;
  explorerUrl?: string;
}

type ActiveTab = 'basic' | 'overrides';
type CredentialInputMode = 'type' | 'raw'; // 'type' = use type selector, 'raw' = raw hex input

interface IValidatorOverrideState {
  index: number;
  amountEth: string;
  useCustomAmount: boolean;
  // Credential override fields
  credentialInputMode: CredentialInputMode;
  credentialType: CredentialType; // '00', '01', '02', 'b0'
  withdrawalAddress: string; // For 0x01/0x02/0xB0 - address or ENS name
  resolvedAddress: string | null; // resolved address when withdrawalAddress is an ENS name
  rawCredentials: string; // For raw mode
  useCustomCredentials: boolean;
}

const DepositGeneratorModal: React.FC<IDepositGeneratorModalProps> = (props) => {
  const { genesisForkVersion, defaultWithdrawalAddress, onClose, onGenerate, domainType, lockBuilderCredentials, faucetEnabled, faucetAmount, explorerUrl } = props;

  const isBuilderMode = domainType === 'builder';
  const modalTitle = isBuilderMode ? 'Generate Builder Deposits' : 'Generate Validator Deposits';
  const keyNoun = isBuilderMode ? 'builder' : 'validator';
  const keyNounCap = isBuilderMode ? 'Builder' : 'Validator';

  const [activeTab, setActiveTab] = useState<ActiveTab>('basic');
  const [isGenerating, setIsGenerating] = useState(false);
  const [generationError, setGenerationError] = useState<string | null>(null);
  const [blsReady, setBlsReady] = useState(false);

  // Basic config state
  // The whole generator config is kept in sessionStorage (devnet tool - per-tab,
  // gone when the browser closes) so reopening the modal in the same session
  // restores it for editing instead of starting over. Keyed per mode so builder
  // and validator setups don't clobber each other; the mnemonic is shared.
  const configCacheKey = `dora_generator_config_${isBuilderMode ? 'builder' : 'deposit'}`;
  const cachedConfig = useMemo(() => {
    try {
      const raw = sessionStorage.getItem(configCacheKey);
      return raw ? JSON.parse(raw) : null;
    } catch {
      return null;
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const [mnemonic, setMnemonic] = useState(() => {
    try {
      return sessionStorage.getItem('dora_generator_mnemonic') || '';
    } catch {
      return '';
    }
  });
  const [showMnemonic, setShowMnemonic] = useState(false);

  useEffect(() => {
    try {
      if (mnemonic.trim() && validateMnemonicWords(mnemonic)) {
        sessionStorage.setItem('dora_generator_mnemonic', mnemonic);
      }
    } catch {
      // storage unavailable (private mode etc.) - non-essential
    }
  }, [mnemonic]);
  const [startIndex, setStartIndex] = useState(Number(cachedConfig?.startIndex) || 0);
  const [validatorCount, setValidatorCount] = useState(Number(cachedConfig?.validatorCount) || 1);
  const [amountEth, setAmountEth] = useState(cachedConfig?.amountEth ?? (isBuilderMode ? '50' : '32'));
  // becomes true once the user edits the amount - stops the dynamic default below
  const [amountTouched, setAmountTouched] = useState(cachedConfig?.amountTouched ?? false);
  const [credentialInputMode, setCredentialInputMode] = useState<CredentialInputMode>(cachedConfig?.credentialInputMode ?? 'type');
  const [credentialType, setCredentialType] = useState<CredentialType>(cachedConfig?.credentialType ?? (lockBuilderCredentials ? 'b0' : '02'));
  const [withdrawalAddress, setWithdrawalAddress] = useState(cachedConfig?.withdrawalAddress ?? (defaultWithdrawalAddress || ''));
  const [rawCredentials, setRawCredentials] = useState(cachedConfig?.rawCredentials ?? '');

  // Per-validator overrides
  const [overrides, setOverrides] = useState<IValidatorOverrideState[]>(
    Array.isArray(cachedConfig?.overrides) ? cachedConfig.overrides : []
  );

  // Persist the generator config so reopening the modal restores it for editing
  useEffect(() => {
    try {
      sessionStorage.setItem(configCacheKey, JSON.stringify({
        startIndex,
        validatorCount,
        amountEth,
        amountTouched,
        credentialInputMode,
        credentialType,
        withdrawalAddress,
        rawCredentials,
        overrides,
      }));
    } catch {
      // storage unavailable (private mode etc.) - non-essential
    }
  }, [configCacheKey, startIndex, validatorCount, amountEth, amountTouched, credentialInputMode, credentialType, withdrawalAddress, rawCredentials, overrides]);

  // Initialize BLS library
  useEffect(() => {
    import('@chainsafe/bls/herumi').then((blsModule) => {
      blsModule.init().then(() => setBlsReady(true));
    });
  }, []);

  // Update overrides when validator count changes
  useEffect(() => {
    setOverrides(prev => {
      const newOverrides: IValidatorOverrideState[] = [];
      for (let i = 0; i < validatorCount; i++) {
        const existing = prev.find(o => o.index === i);
        if (existing) {
          newOverrides.push(existing);
        } else {
          newOverrides.push({
            index: i,
            amountEth: amountEth,
            useCustomAmount: false,
            credentialInputMode: 'type',
            credentialType: credentialType, // Default to parent's credential type
            withdrawalAddress: '',
            resolvedAddress: null,
            rawCredentials: '',
            useCustomCredentials: false,
          });
        }
      }
      return newOverrides;
    });
  }, [validatorCount, amountEth, credentialType]);

  // Enforce the 32 ETH cap on rows whose credential type is 0x00/0x01, however the
  // type got there (dropdown, default seeding, cached restore). The cap materializes
  // as the row's own editable amount: it snaps to 32, can be lowered, and anything
  // above 32 snaps back.
  useEffect(() => {
    if (isBuilderMode) return;
    setOverrides(prev => {
      let changed = false;
      const next = prev.map(o => {
        if (!o.useCustomCredentials || o.credentialInputMode !== 'type') return o;
        if (o.credentialType !== '00' && o.credentialType !== '01') return o;
        const effective = parseFloat(o.useCustomAmount ? o.amountEth : amountEth);
        if (isNaN(effective) || effective <= 32) return o;
        changed = true;
        return { ...o, useCustomAmount: true, amountEth: '32' };
      });
      return changed ? next : prev;
    });
  }, [overrides, amountEth, isBuilderMode]);

  // Validation - only accept 24-word mnemonics
  const mnemonicValid = useMemo(() => {
    if (!mnemonic.trim()) return null;
    const words = mnemonic.trim().split(/\s+/).filter(w => w.length > 0);
    if (words.length !== 24) return false;
    return validateMnemonicWords(mnemonic);
  }, [mnemonic]);

  // ENS support for the withdrawal address field: the shared EnsAddressInput
  // resolves dotted non-hex inputs and reports the resolution state here.
  const trimmedAddressInput = withdrawalAddress.trim();
  const isEnsNameInput = trimmedAddressInput.includes('.') && !isAddress(trimmedAddressInput);
  const [ensState, setEnsState] = useState<IEnsResolutionState>(emptyEnsState);

  // Effective withdrawal address (typed address or resolved ENS name) and its
  // reverse-resolved ENS name, used to preview the default credentials in the
  // overrides tab. The typed ENS name wins over a reverse lookup.
  const effectiveAddress = isEnsNameInput && ensState.address
    ? ensState.address
    : isAddress(trimmedAddressInput) ? trimmedAddressInput : null;
  const reverseEnsName = useEnsLookup(effectiveAddress);
  const effectiveEnsName = (isEnsNameInput ? ensState.name : null) ?? reverseEnsName;

  // Human-readable preview of the default credentials, shown for override rows
  // that don't set their own (e.g. "0xB0 bbusa.eth").
  const shortEffectiveAddress = effectiveAddress
    ? `${effectiveAddress.substring(0, 8)}…${effectiveAddress.substring(effectiveAddress.length - 6)}`
    : null;
  const defaultCredsLabel = credentialInputMode === 'raw'
    ? (rawCredentials ? `raw ${rawCredentials.replace(/^0x/, '').substring(0, 8)}…` : 'raw credentials')
    : credentialType === '00'
      ? '0x00 (derived key)'
      : `0x${credentialType.toUpperCase()} ${effectiveEnsName ?? shortEffectiveAddress ?? '(no address set)'}`;

  // For type mode: 0x00 doesn't need address, 0x01/0x02 need address (or a resolvable ENS name)
  const addressValid = useMemo(() => {
    if (credentialInputMode !== 'type') return true;
    if (credentialType === '00') return true; // 0x00 uses derived withdrawal key
    if (!withdrawalAddress) return null;
    if (isAddress(withdrawalAddress)) return true;
    if (isEnsNameInput) {
      if (ensState.resolving) return null;
      return ensState.address !== null;
    }
    return false;
  }, [credentialInputMode, credentialType, withdrawalAddress, isEnsNameInput, ensState]);

  const rawCredentialsValid = useMemo(() => {
    if (credentialInputMode !== 'raw') return true;
    if (!rawCredentials) return null;
    return validateWithdrawalCredentials(rawCredentials);
  }, [credentialInputMode, rawCredentials]);

  // 0x00/0x01 credentials run with a 32 ETH max effective balance, so cap those deposits
  const maxAmountEth = !isBuilderMode && (credentialType === '00' || credentialType === '01') ? 32 : 2048;

  const amountValid = useMemo(() => {
    const amount = parseFloat(amountEth);
    return !isNaN(amount) && amount >= 1 && amount <= maxAmountEth;
  }, [amountEth, maxAmountEth]);

  const canGenerate = useMemo(() => {
    if (!blsReady || mnemonicValid !== true || validatorCount <= 0 || !amountValid) {
      return false;
    }
    if (credentialInputMode === 'type') {
      // For 0x00: no address needed
      // For 0x01/0x02: address required
      if (credentialType === '00') return true;
      return addressValid === true;
    } else {
      // Raw mode
      return rawCredentialsValid === true;
    }
  }, [blsReady, mnemonicValid, validatorCount, amountValid, credentialInputMode, credentialType, addressValid, rawCredentialsValid]);

  const handleOverrideChange = useCallback((index: number, field: keyof IValidatorOverrideState, value: any) => {
    setOverrides(prev => prev.map(o => {
      if (o.index !== index) return o;
      return { ...o, [field]: value };
    }));
  }, []);

  const handleGenerate = async () => {
    if (!canGenerate) return;

    setIsGenerating(true);
    setGenerationError(null);

    try {
      // Build default credential config (ENS names use their resolved address)
      const effectiveWithdrawalAddress = isEnsNameInput && ensState.address ? ensState.address : withdrawalAddress;
      const defaultCredentialConfig: WithdrawalCredentialConfig = {
        type: credentialType,
        address: credentialType !== '00' ? effectiveWithdrawalAddress : undefined,
      };

      // Build overrides
      const validatorOverrides: ValidatorOverride[] = [];
      for (const override of overrides) {
        if (!override.useCustomAmount && !override.useCustomCredentials) continue;

        const validatorOverride: ValidatorOverride = { index: override.index };

        if (override.useCustomAmount) {
          const amount = parseFloat(override.amountEth);
          if (isNaN(amount) || amount < 1) {
            throw new Error(`Invalid amount for ${keyNoun} #${override.index}`);
          }
          validatorOverride.amount = ethToGwei(amount);
        }

        if (override.useCustomCredentials) {
          if (override.credentialInputMode === 'type') {
            // Type-based credential override (ENS names use their resolved address)
            const overrideAddress = isAddress(override.withdrawalAddress)
              ? override.withdrawalAddress
              : override.resolvedAddress;
            if (override.credentialType !== '00' && !overrideAddress) {
              throw new Error(`Invalid withdrawal address for ${keyNoun} #${override.index}`);
            }
            validatorOverride.credentialConfig = {
              type: override.credentialType,
              address: override.credentialType !== '00' ? overrideAddress : undefined,
            };
          } else {
            // Raw credential override
            if (!validateWithdrawalCredentials(override.rawCredentials)) {
              throw new Error(`Invalid raw credentials for ${keyNoun} #${override.index}`);
            }
            validatorOverride.rawCredentials = override.rawCredentials.startsWith('0x')
              ? override.rawCredentials
              : `0x${override.rawCredentials}`;
          }
        }

        // safety net: 0x00/0x01 credentials cap the deposit at exactly 32 ETH
        const effectiveType = override.useCustomCredentials && override.credentialInputMode === 'type'
          ? override.credentialType
          : credentialInputMode === 'type' ? credentialType : null;
        if (effectiveType === '01' || effectiveType === '00') {
          const capGwei = ethToGwei(32);
          const effectiveAmountGwei = validatorOverride.amount ?? ethToGwei(parseFloat(amountEth));
          if (effectiveAmountGwei > capGwei) {
            validatorOverride.amount = capGwei;
          }
        }

        validatorOverrides.push(validatorOverride);
      }

      const config: GeneratorConfig = {
        mnemonic,
        startIndex,
        validatorCount,
        defaultAmountGwei: ethToGwei(parseFloat(amountEth)),
        defaultCredentialConfig,
        useRawCredentials: credentialInputMode === 'raw',
        defaultRawCredentials: credentialInputMode === 'raw'
          ? (rawCredentials.startsWith('0x') ? rawCredentials : `0x${rawCredentials}`)
          : undefined,
        overrides: validatorOverrides,
      };

      const deposits = await generateDeposits(config, genesisForkVersion, domainType ?? 'deposit');
      onGenerate(deposits, mnemonic.trim().toLowerCase().replace(/\s+/g, ' '));
    } catch (error) {
      setGenerationError(error instanceof Error ? error.message : String(error));
    } finally {
      setIsGenerating(false);
    }
  };

  // Get mnemonic word count
  const wordCount = useMemo(() => {
    const words = mnemonic.trim().split(/\s+/).filter(w => w.length > 0);
    return words.length;
  }, [mnemonic]);

  // Execution layer wallet derived from the mnemonic (m/44'/60'/0'/0/0).
  // Shown so the user can fund it (via the faucet on devnets) and use it to submit the deposits.
  const derivedWalletAddress = useMemo(() => {
    if (mnemonicValid !== true) return null;
    try {
      return mnemonicToAccount(mnemonic.trim().toLowerCase().replace(/\s+/g, ' ')).address;
    } catch {
      return null;
    }
  }, [mnemonicValid, mnemonic]);

  // Dynamic deposit amount default: spread the funding wallet's balance across the
  // requested deposits (connected wallet first, else the mnemonic-derived wallet).
  // Floors: builders need >= 1 ETH, validators >= 32 ETH. Stops once the user edits
  // the amount manually.
  const minAmountEth = isBuilderMode ? 1 : 32;
  const fundingAddress = (defaultWithdrawalAddress || derivedWalletAddress || undefined) as `0x${string}` | undefined;
  const fundingBalance = useBalance({
    address: fundingAddress,
    query: { refetchInterval: 12000 },
  });

  // live balance of the mnemonic-derived wallet, shown next to its address
  const derivedBalance = useBalance({
    address: (derivedWalletAddress ?? undefined) as `0x${string}` | undefined,
    query: { refetchInterval: 12000 },
  });

  useEffect(() => {
    if (amountTouched) return;
    const balanceWei = fundingBalance.data?.value;
    if (balanceWei === undefined || balanceWei <= 0n) return;

    const reserveWei = 10n ** 17n; // keep 0.1 ETH for gas & queue fees
    const usableWei = balanceWei > reserveWei ? balanceWei - reserveWei : 0n;
    let perDeposit = Number(usableWei / BigInt(Math.max(1, validatorCount)) / (10n ** 18n));
    if (perDeposit < minAmountEth) perDeposit = minAmountEth;
    if (perDeposit > maxAmountEth) perDeposit = maxAmountEth;
    setAmountEth(String(perDeposit));
  }, [fundingBalance.data?.value, validatorCount, amountTouched, minAmountEth, maxAmountEth]);

  if (!blsReady) {
    return (
      <Modal show={true} onHide={onClose} size="lg" className="deposit-generator-modal">
        <Modal.Header closeButton>
          <Modal.Title>
            <i className="fa fa-magic me-2"></i>
            {modalTitle}
          </Modal.Title>
        </Modal.Header>
        <Modal.Body>
          <div className="text-center py-4">
            <span className="spinner-border spinner-border-sm me-2"></span>
            Initializing cryptographic library...
          </div>
        </Modal.Body>
      </Modal>
    );
  }

  return (
    <Modal show={true} onHide={onClose} size="lg" className="deposit-generator-modal">
      <Modal.Header closeButton>
        <Modal.Title>
          <i className="fa fa-magic me-2"></i>
          {modalTitle}
        </Modal.Title>
      </Modal.Header>
      <Modal.Body>
        {/* Security Warning */}
        <div className="alert alert-danger security-warning mb-3">
          <div className="d-flex align-items-start">
            <i className="fa fa-exclamation-triangle fa-2x me-3 mt-1 warning-icon"></i>
            <div>
              <strong>DEVNET ONLY - SECURITY WARNING</strong>
              <p className="mb-0 mt-1">
                Never enter your mnemonic on any website for mainnet {keyNoun}s.
                This tool is <strong>only</strong> for testing on development networks.
                Your mnemonic grants full control over your {keyNoun}s.
              </p>
            </div>
          </div>
        </div>

        {/* Tab Navigation */}
        <ul className="nav nav-tabs mb-3">
          <li className="nav-item">
            <button
              className={`nav-link ${activeTab === 'basic' ? 'active' : ''}`}
              onClick={() => setActiveTab('basic')}
            >
              <i className="fa fa-cog me-1"></i> Basic Config
            </button>
          </li>
          <li className="nav-item">
            <button
              className={`nav-link ${activeTab === 'overrides' ? 'active' : ''}`}
              onClick={() => setActiveTab('overrides')}
            >
              <i className="fa fa-list me-1"></i> Per-{keyNounCap} Overrides
            </button>
          </li>
        </ul>

        {/* Generation Error */}
        {generationError && (
          <div className="alert alert-danger mb-3">
            <i className="fa fa-times-circle me-2"></i>
            {generationError}
          </div>
        )}

        {/* Basic Config Tab */}
        {activeTab === 'basic' && (
          <div>
            {/* Mnemonic Input */}
            <div className="mb-3">
              <label className="form-label d-flex justify-content-between align-items-center">
                <span>Mnemonic Phrase</span>
                <span className="d-inline-flex gap-2">
                  <button
                    type="button"
                    className="btn btn-sm btn-outline-primary"
                    onClick={() => {
                      setMnemonic(generateRandomMnemonic());
                      setShowMnemonic(true);
                    }}
                    title="Generate a new random 24 word mnemonic"
                  >
                    <i className="fa fa-dice me-1"></i>
                    Generate Random
                  </button>
                  <button
                    type="button"
                    className="btn btn-sm btn-outline-secondary mnemonic-toggle"
                    onClick={() => setShowMnemonic(!showMnemonic)}
                  >
                    <i className={`fa ${showMnemonic ? 'fa-eye-slash' : 'fa-eye'} me-1`}></i>
                    {showMnemonic ? 'Hide' : 'Show'}
                  </button>
                </span>
              </label>
              <textarea
                className={`form-control mnemonic-input font-monospace ${
                  mnemonicValid === false ? 'is-invalid' : mnemonicValid === true ? 'is-valid' : ''
                }`}
                rows={3}
                placeholder="Enter your 24 word mnemonic phrase..."
                value={showMnemonic ? mnemonic : mnemonic.replace(/\S/g, '*')}
                onChange={(e) => setMnemonic(showMnemonic ? e.target.value : mnemonic)}
                onFocus={() => !showMnemonic && setShowMnemonic(true)}
                style={{ WebkitTextSecurity: showMnemonic ? 'none' : 'disc' } as React.CSSProperties}
              />
              <div className="form-text">
                {wordCount > 0 && (
                  <span className={mnemonicValid === true ? 'text-success' : mnemonicValid === false ? 'text-danger' : ''}>
                    {wordCount}/24 words entered
                    {mnemonicValid === true && ' - Valid mnemonic'}
                    {mnemonicValid === false && wordCount !== 24 && ' - Must be exactly 24 words'}
                    {mnemonicValid === false && wordCount === 24 && ' - Invalid mnemonic'}
                  </span>
                )}
              </div>
              {derivedWalletAddress && (
                <div className="form-text d-flex align-items-center gap-2 flex-wrap">
                  <span>
                    Wallet address (m/44'/60'/0'/0/0):{' '}
                    <span className="font-monospace">{derivedWalletAddress}</span>
                    {derivedBalance.data !== undefined && (
                      <span className="ms-1">(balance: {toReadableAmount(derivedBalance.data.value, 18, 'ETH', 4)})</span>
                    )}
                  </span>
                  {faucetEnabled && (
                    <FaucetButton
                      address={derivedWalletAddress}
                      amount={faucetAmount || 50}
                      explorerUrl={explorerUrl}
                    />
                  )}
                </div>
              )}
            </div>

            {/* Index and Count */}
            <div className="row mb-3">
              <div className="col-6">
                <label className="form-label">Start Index</label>
                <input
                  type="number"
                  className="form-control"
                  min={0}
                  value={startIndex}
                  onChange={(e) => setStartIndex(Math.max(0, parseInt(e.target.value) || 0))}
                />
                <div className="form-text">First {keyNoun} index to derive</div>
              </div>
              <div className="col-6">
                <label className="form-label">{keyNounCap} Count</label>
                <input
                  type="number"
                  className="form-control"
                  min={1}
                  max={100}
                  value={validatorCount}
                  onChange={(e) => setValidatorCount(Math.max(1, Math.min(100, parseInt(e.target.value) || 1)))}
                />
                <div className="form-text">Number of {keyNoun}s to generate</div>
              </div>
            </div>

            {/* Amount */}
            <div className="mb-3">
              <label className="form-label">Deposit Amount (ETH)</label>
              <input
                type="number"
                className={`form-control ${!amountValid ? 'is-invalid' : ''}`}
                min={1}
                max={maxAmountEth}
                step="1"
                value={amountEth}
                onChange={(e) => {
                  setAmountTouched(true);
                  setAmountEth(e.target.value);
                }}
              />
              {!amountValid && (
                <div className="invalid-feedback">
                  Amount must be between 1 and {maxAmountEth} ETH
                  {maxAmountEth === 32 && ' (0x00/0x01 credentials cap the deposit at 32 ETH)'}
                </div>
              )}
              {!amountTouched && fundingBalance.data !== undefined && (
                <div className="form-text">
                  Auto: {(Number(fundingBalance.data.value) / 1e18).toFixed(2)} ETH on{' '}
                  <span className="font-monospace">{fundingAddress?.substring(0, 10)}…</span> / {validatorCount} deposit{validatorCount !== 1 ? 's' : ''} (min {minAmountEth} ETH)
                </div>
              )}
            </div>

            {/* Withdrawal Credentials */}
            <div className="mb-3">
              <label className="form-label d-flex justify-content-between align-items-center">
                <span>Withdrawal Credentials</span>
                <div className="form-check form-switch mb-0">
                  <input
                    className="form-check-input"
                    type="checkbox"
                    id="advancedCredentials"
                    checked={credentialInputMode === 'raw'}
                    onChange={(e) => setCredentialInputMode(e.target.checked ? 'raw' : 'type')}
                  />
                  <label className="form-check-label" htmlFor="advancedCredentials">
                    Advanced (raw)
                  </label>
                </div>
              </label>

              {credentialInputMode === 'type' ? (
                <>
                  <div className="row mb-2">
                    <div className={credentialType === '00' ? 'col-12' : 'col-4'}>
                      <select
                        className="form-select"
                        value={credentialType}
                        disabled={lockBuilderCredentials}
                        onChange={(e) => {
                          const newType = e.target.value as CredentialType;
                          setCredentialType(newType);
                          // 0x00/0x01 cap the deposit at 32 ETH - snap down when switching
                          if (!isBuilderMode && (newType === '00' || newType === '01') && parseFloat(amountEth) > 32) {
                            setAmountEth('32');
                          }
                        }}
                      >
                        {lockBuilderCredentials || isBuilderMode ? (
                          <option value="b0">0xB0 - Builder</option>
                        ) : (
                          <>
                            <option value="00">0x00 - BLS (derived)</option>
                            <option value="01">0x01 - Execution</option>
                            <option value="02">0x02 - Compounding</option>
                          </>
                        )}
                      </select>
                    </div>
                    {credentialType !== '00' && (
                      <div className="col-8">
                        <EnsAddressInput
                          value={withdrawalAddress}
                          onChange={setWithdrawalAddress}
                          onEnsState={setEnsState}
                        />
                      </div>
                    )}
                  </div>
                  <div className="form-text">
                    {credentialType === '00'
                      ? 'Uses withdrawal key derived from mnemonic (path: m/12381/3600/i/0)'
                      : 'Withdrawals will be sent to this address'}
                  </div>
                </>
              ) : (
                <>
                  <input
                    type="text"
                    className={`form-control font-monospace ${
                      rawCredentialsValid === false ? 'is-invalid' : rawCredentialsValid === true ? 'is-valid' : ''
                    }`}
                    placeholder="0x01000000000000000000000000..."
                    value={rawCredentials}
                    onChange={(e) => setRawCredentials(e.target.value)}
                  />
                  {rawCredentialsValid === false && (
                    <div className="invalid-feedback">Must be 32 bytes (64 hex characters)</div>
                  )}
                  <div className="form-text">
                    Enter raw 32-byte withdrawal credentials
                  </div>
                </>
              )}
            </div>
          </div>
        )}

        {/* Per-Validator Overrides Tab */}
        {activeTab === 'overrides' && (
          <div>
            <p className="text-muted mb-3">
              Override deposit amount or withdrawal credentials for individual {keyNoun}s.
              {' '}{keyNounCap}s without overrides will use the default settings from Basic Config.
            </p>

            {validatorCount === 0 ? (
              <div className="alert alert-info">
                Set a {keyNoun} count in Basic Config to configure overrides.
              </div>
            ) : (
              <div className="table-responsive">
                <table className="table table-sm validator-override-table">
                  <thead>
                    <tr>
                      <th style={{ width: '80px' }}>Index</th>
                      <th style={{ width: '150px' }}>Amount (ETH)</th>
                      <th>Withdrawal Credentials</th>
                    </tr>
                  </thead>
                  <tbody>
                    {overrides.map((override) => (
                      <tr key={override.index} className={override.useCustomAmount || override.useCustomCredentials ? 'override-active' : ''}>
                        <td className="align-middle">
                          <strong>#{startIndex + override.index}</strong>
                        </td>
                        <td>
                          <div className="d-flex align-items-center gap-2">
                            <input
                              type="checkbox"
                              className="form-check-input"
                              checked={override.useCustomAmount}
                              onChange={(e) => handleOverrideChange(override.index, 'useCustomAmount', e.target.checked)}
                              title="Override amount"
                            />
                            <input
                              type="number"
                              className="form-control form-control-sm"
                              value={override.useCustomAmount ? override.amountEth : amountEth}
                              onChange={(e) => handleOverrideChange(override.index, 'amountEth', e.target.value)}
                              disabled={!override.useCustomAmount}
                              min={1}
                              max={2048}
                              style={{ width: '100px' }}
                            />
                          </div>
                        </td>
                        <td>
                          <div className="d-flex align-items-center gap-2">
                            <input
                              type="checkbox"
                              className="form-check-input"
                              checked={override.useCustomCredentials}
                              onChange={(e) => {
                                const checked = e.target.checked;
                                handleOverrideChange(override.index, 'useCustomCredentials', checked);
                                // seed the row with the current defaults so editing starts
                                // from them instead of an empty field
                                if (checked) {
                                  if (credentialInputMode === 'raw') {
                                    if (!override.rawCredentials) {
                                      handleOverrideChange(override.index, 'credentialInputMode', 'raw');
                                      handleOverrideChange(override.index, 'rawCredentials', rawCredentials);
                                    }
                                  } else if (!override.withdrawalAddress) {
                                    handleOverrideChange(override.index, 'credentialType', credentialType);
                                    handleOverrideChange(override.index, 'withdrawalAddress', withdrawalAddress);
                                  }
                                }
                              }}
                              title="Override credentials"
                            />
                            {override.useCustomCredentials ? (
                              <div className="d-flex gap-1 flex-grow-1 align-items-center">
                                {/* Input mode selector */}
                                <select
                                  className="form-select form-select-sm flex-shrink-0"
                                  value={override.credentialInputMode}
                                  onChange={(e) => handleOverrideChange(override.index, 'credentialInputMode', e.target.value)}
                                  style={{ width: '90px' }}
                                >
                                  <option value="type">Type</option>
                                  <option value="raw">Raw</option>
                                </select>

                                {override.credentialInputMode === 'type' ? (
                                  <>
                                    {/* Credential type selector */}
                                    <select
                                      className="form-select form-select-sm flex-shrink-0"
                                      value={override.credentialType}
                                      onChange={(e) => handleOverrideChange(override.index, 'credentialType', e.target.value)}
                                      style={{ width: '80px' }}
                                    >
                                      {isBuilderMode ? (
                                        <option value="b0">0xB0</option>
                                      ) : (
                                        <>
                                          <option value="00">0x00</option>
                                          <option value="01">0x01</option>
                                          <option value="02">0x02</option>
                                        </>
                                      )}
                                    </select>
                                    {/* Address input (only for 0x01/0x02/0xB0), accepts ENS names */}
                                    {override.credentialType !== '00' && (
                                      <EnsAddressInput
                                        small
                                        value={override.withdrawalAddress}
                                        onChange={(value) => handleOverrideChange(override.index, 'withdrawalAddress', value)}
                                        onEnsState={(state) => handleOverrideChange(override.index, 'resolvedAddress', state.address)}
                                      />
                                    )}
                                    {override.credentialType === '00' && (
                                      <span className="text-muted flex-shrink-0" style={{ fontSize: '0.85em' }}>
                                        (derived)
                                      </span>
                                    )}
                                  </>
                                ) : (
                                  /* Raw credentials input */
                                  <input
                                    type="text"
                                    className="form-control form-control-sm font-monospace flex-grow-1"
                                    placeholder="0x01000000..."
                                    value={override.rawCredentials}
                                    onChange={(e) => handleOverrideChange(override.index, 'rawCredentials', e.target.value)}
                                    style={{ minWidth: '100px' }}
                                  />
                                )}
                              </div>
                            ) : (
                              <span className="text-muted" title="Default withdrawal credentials from Basic Config">{defaultCredsLabel}</span>
                            )}
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        )}
      </Modal.Body>
      <Modal.Footer>
        <button className="btn btn-secondary" onClick={onClose} disabled={isGenerating}>
          Cancel
        </button>
        <button
          className="btn btn-primary"
          onClick={handleGenerate}
          disabled={!canGenerate || isGenerating}
        >
          {isGenerating ? (
            <span>
              <span className="spinner-border spinner-border-sm me-2"></span>
              Generating...
            </span>
          ) : (
            <span>
              <i className="fa fa-magic me-1"></i>
              Generate {validatorCount} {isBuilderMode ? 'Builder ' : ''}Deposit{validatorCount !== 1 ? 's' : ''}
            </span>
          )}
        </button>
      </Modal.Footer>
    </Modal>
  );
};

export default DepositGeneratorModal;
