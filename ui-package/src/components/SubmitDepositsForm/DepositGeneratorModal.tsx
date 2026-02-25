import React, { useState, useEffect, useMemo, useCallback } from 'react';
import { Modal } from 'react-bootstrap';
import { isAddress } from 'viem';

import { IDeposit } from './DepositsTable';
import {
  validateMnemonicWords,
  validateWithdrawalCredentials,
  generateDeposits,
  ethToGwei,
  GeneratorConfig,
  ValidatorOverride,
  CredentialType,
  WithdrawalCredentialConfig,
} from './DepositGenerator';

interface IDepositGeneratorModalProps {
  genesisForkVersion: string;
  defaultWithdrawalAddress?: string;
  onClose: () => void;
  onGenerate: (deposits: IDeposit[]) => void;
}

type ActiveTab = 'basic' | 'overrides';
type CredentialInputMode = 'type' | 'raw'; // 'type' = use type selector, 'raw' = raw hex input

interface IValidatorOverrideState {
  index: number;
  amountEth: string;
  useCustomAmount: boolean;
  // Credential override fields
  credentialInputMode: CredentialInputMode;
  credentialType: CredentialType; // '00', '01', '02', '03'
  withdrawalAddress: string; // For 0x01/0x02/0x03
  rawCredentials: string; // For raw mode
  useCustomCredentials: boolean;
}

const DepositGeneratorModal: React.FC<IDepositGeneratorModalProps> = (props) => {
  const { genesisForkVersion, defaultWithdrawalAddress, onClose, onGenerate } = props;

  const [activeTab, setActiveTab] = useState<ActiveTab>('basic');
  const [isGenerating, setIsGenerating] = useState(false);
  const [generationError, setGenerationError] = useState<string | null>(null);
  const [blsReady, setBlsReady] = useState(false);

  // Basic config state
  const [mnemonic, setMnemonic] = useState('');
  const [showMnemonic, setShowMnemonic] = useState(false);
  const [startIndex, setStartIndex] = useState(0);
  const [validatorCount, setValidatorCount] = useState(1);
  const [amountEth, setAmountEth] = useState('32');
  const [credentialInputMode, setCredentialInputMode] = useState<CredentialInputMode>('type');
  const [credentialType, setCredentialType] = useState<CredentialType>('01');
  const [withdrawalAddress, setWithdrawalAddress] = useState(defaultWithdrawalAddress || '');
  const [rawCredentials, setRawCredentials] = useState('');

  // Per-validator overrides
  const [overrides, setOverrides] = useState<IValidatorOverrideState[]>([]);

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
            rawCredentials: '',
            useCustomCredentials: false,
          });
        }
      }
      return newOverrides;
    });
  }, [validatorCount, amountEth, credentialType]);

  // Validation - only accept 24-word mnemonics
  const mnemonicValid = useMemo(() => {
    if (!mnemonic.trim()) return null;
    const words = mnemonic.trim().split(/\s+/).filter(w => w.length > 0);
    if (words.length !== 24) return false;
    return validateMnemonicWords(mnemonic);
  }, [mnemonic]);

  // For type mode: 0x00 doesn't need address, 0x01/0x02 need address
  const addressValid = useMemo(() => {
    if (credentialInputMode !== 'type') return true;
    if (credentialType === '00') return true; // 0x00 uses derived withdrawal key
    if (!withdrawalAddress) return null;
    return isAddress(withdrawalAddress);
  }, [credentialInputMode, credentialType, withdrawalAddress]);

  const rawCredentialsValid = useMemo(() => {
    if (credentialInputMode !== 'raw') return true;
    if (!rawCredentials) return null;
    return validateWithdrawalCredentials(rawCredentials);
  }, [credentialInputMode, rawCredentials]);

  const amountValid = useMemo(() => {
    const amount = parseFloat(amountEth);
    return !isNaN(amount) && amount >= 1 && amount <= 2048;
  }, [amountEth]);

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
      // Build default credential config
      const defaultCredentialConfig: WithdrawalCredentialConfig = {
        type: credentialType,
        address: credentialType !== '00' ? withdrawalAddress : undefined,
      };

      // Build overrides
      const validatorOverrides: ValidatorOverride[] = [];
      for (const override of overrides) {
        if (!override.useCustomAmount && !override.useCustomCredentials) continue;

        const validatorOverride: ValidatorOverride = { index: override.index };

        if (override.useCustomAmount) {
          const amount = parseFloat(override.amountEth);
          if (isNaN(amount) || amount < 1) {
            throw new Error(`Invalid amount for validator #${override.index}`);
          }
          validatorOverride.amount = ethToGwei(amount);
        }

        if (override.useCustomCredentials) {
          if (override.credentialInputMode === 'type') {
            // Type-based credential override
            if (override.credentialType !== '00' && !isAddress(override.withdrawalAddress)) {
              throw new Error(`Invalid withdrawal address for validator #${override.index}`);
            }
            validatorOverride.credentialConfig = {
              type: override.credentialType,
              address: override.credentialType !== '00' ? override.withdrawalAddress : undefined,
            };
          } else {
            // Raw credential override
            if (!validateWithdrawalCredentials(override.rawCredentials)) {
              throw new Error(`Invalid raw credentials for validator #${override.index}`);
            }
            validatorOverride.rawCredentials = override.rawCredentials.startsWith('0x')
              ? override.rawCredentials
              : `0x${override.rawCredentials}`;
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

      const deposits = await generateDeposits(config, genesisForkVersion);
      onGenerate(deposits);
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

  if (!blsReady) {
    return (
      <Modal show={true} onHide={onClose} size="lg" className="deposit-generator-modal">
        <Modal.Header closeButton>
          <Modal.Title>
            <i className="fa fa-magic me-2"></i>
            Generate Validator Deposits
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
          Generate Validator Deposits
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
                Never enter your mnemonic on any website for mainnet validators.
                This tool is <strong>only</strong> for testing on development networks.
                Your mnemonic grants full control over your validators.
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
              <i className="fa fa-list me-1"></i> Per-Validator Overrides
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
                <button
                  type="button"
                  className="btn btn-sm btn-outline-secondary mnemonic-toggle"
                  onClick={() => setShowMnemonic(!showMnemonic)}
                >
                  <i className={`fa ${showMnemonic ? 'fa-eye-slash' : 'fa-eye'} me-1`}></i>
                  {showMnemonic ? 'Hide' : 'Show'}
                </button>
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
                <div className="form-text">First validator index to derive</div>
              </div>
              <div className="col-6">
                <label className="form-label">Validator Count</label>
                <input
                  type="number"
                  className="form-control"
                  min={1}
                  max={100}
                  value={validatorCount}
                  onChange={(e) => setValidatorCount(Math.max(1, Math.min(100, parseInt(e.target.value) || 1)))}
                />
                <div className="form-text">Number of validators to generate</div>
              </div>
            </div>

            {/* Amount */}
            <div className="mb-3">
              <label className="form-label">Deposit Amount (ETH)</label>
              <input
                type="number"
                className={`form-control ${!amountValid ? 'is-invalid' : ''}`}
                min={1}
                max={2048}
                step="1"
                value={amountEth}
                onChange={(e) => setAmountEth(e.target.value)}
              />
              {!amountValid && (
                <div className="invalid-feedback">Amount must be between 1 and 2048 ETH</div>
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
                        onChange={(e) => setCredentialType(e.target.value as CredentialType)}
                      >
                        <option value="00">0x00 - BLS (derived)</option>
                        <option value="01">0x01 - Execution</option>
                        <option value="02">0x02 - Compounding</option>
                        <option value="03">0x03 - Builder</option>
                      </select>
                    </div>
                    {credentialType !== '00' && (
                      <div className="col-8">
                        <input
                          type="text"
                          className={`form-control font-monospace ${
                            addressValid === false ? 'is-invalid' : addressValid === true ? 'is-valid' : ''
                          }`}
                          placeholder="0x..."
                          value={withdrawalAddress}
                          onChange={(e) => setWithdrawalAddress(e.target.value)}
                        />
                        {addressValid === false && (
                          <div className="invalid-feedback">Please enter a valid Ethereum address</div>
                        )}
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
              Override deposit amount or withdrawal credentials for individual validators.
              Validators without overrides will use the default settings from Basic Config.
            </p>

            {validatorCount === 0 ? (
              <div className="alert alert-info">
                Set a validator count in Basic Config to configure overrides.
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
                              onChange={(e) => handleOverrideChange(override.index, 'useCustomCredentials', e.target.checked)}
                              title="Override credentials"
                            />
                            {override.useCustomCredentials ? (
                              <div className="d-flex gap-1 flex-grow-1 align-items-center">
                                {/* Input mode selector */}
                                <select
                                  className="form-select form-select-sm flex-shrink-0"
                                  value={override.credentialInputMode}
                                  onChange={(e) => handleOverrideChange(override.index, 'credentialInputMode', e.target.value)}
                                  style={{ width: '70px' }}
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
                                      <option value="00">0x00</option>
                                      <option value="01">0x01</option>
                                      <option value="02">0x02</option>
                                      <option value="03">0x03</option>
                                    </select>
                                    {/* Address input (only for 0x01/0x02/0x03) */}
                                    {override.credentialType !== '00' && (
                                      <input
                                        type="text"
                                        className="form-control form-control-sm font-monospace flex-grow-1"
                                        placeholder="0x..."
                                        value={override.withdrawalAddress}
                                        onChange={(e) => handleOverrideChange(override.index, 'withdrawalAddress', e.target.value)}
                                        style={{ minWidth: '100px' }}
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
                              <span className="text-muted">(use default)</span>
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
              Generate {validatorCount} Deposit{validatorCount !== 1 ? 's' : ''}
            </span>
          )}
        </button>
      </Modal.Footer>
    </Modal>
  );
};

export default DepositGeneratorModal;
