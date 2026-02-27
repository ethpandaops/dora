import React, { useState, useEffect, useCallback } from 'react';
import { useAccount, useWriteContract, usePublicClient } from 'wagmi';
import { Modal } from 'react-bootstrap';
import { isAddress } from 'viem';

import {
  GatingContractAbi,
  DEFAULT_ADMIN_ROLE,
  DEPOSIT_TYPES,
  DEPOSIT_TYPE_LABELS,
  GatingContractData,
} from './GatingContract';
import {
  batchedEthCalls,
  encodeCall,
  decodeCallResult,
  RpcClient
} from '../../utils/RpcQueue';

interface IGatingManageModalProps {
  gatingData: GatingContractData;
  chainId: number;
  onClose: () => void;
  onSuccess: () => void;
}

type ActiveSection = 'mint' | 'admin' | 'config';

const GatingManageModal: React.FC<IGatingManageModalProps> = (props) => {
  const { gatingData, chainId, onClose, onSuccess } = props;
  const { address: walletAddress, chain } = useAccount();
  const client = usePublicClient({ chainId });

  const [activeSection, setActiveSection] = useState<ActiveSection>('mint');
  const [errorModal, setErrorModal] = useState<string | null>(null);
  const [successMessage, setSuccessMessage] = useState<string | null>(null);

  // Mint section state
  const [mintAddress, setMintAddress] = useState('');
  const [mintAmount, setMintAmount] = useState('1');

  // Admin section state
  const [adminAddress, setAdminAddress] = useState('');
  const [isCheckingSticky, setIsCheckingSticky] = useState(false);
  const [isStickyRole, setIsStickyRole] = useState(false);
  const [hasAdminRole, setHasAdminRole] = useState(false);

  // Config section state
  const [selectedDepositType, setSelectedDepositType] = useState<number>(DEPOSIT_TYPES.TOPUP);
  const [configBlocked, setConfigBlocked] = useState(false);
  const [configNoToken, setConfigNoToken] = useState(false);

  const mintRequest = useWriteContract();
  const grantAdminRequest = useWriteContract();
  const revokeAdminRequest = useWriteContract();
  const setConfigRequest = useWriteContract();

  // Load current config when deposit type changes
  useEffect(() => {
    const config = gatingData.depositConfigs.get(selectedDepositType);
    if (config) {
      setConfigBlocked(config.blocked);
      setConfigNoToken(config.noToken);
    }
  }, [selectedDepositType, gatingData.depositConfigs]);

  // Check admin role and sticky status when admin address changes
  useEffect(() => {
    const checkAdminStatus = async () => {
      if (!client || !chainId || !isAddress(adminAddress)) {
        setHasAdminRole(false);
        setIsStickyRole(false);
        return;
      }

      setIsCheckingSticky(true);
      try {
        // Use batched calls through central queue
        const calls = [
          {
            target: gatingData.gatingContractAddress as `0x${string}`,
            callData: encodeCall(GatingContractAbi, 'hasRole', [DEFAULT_ADMIN_ROLE, adminAddress])
          },
          {
            target: gatingData.gatingContractAddress as `0x${string}`,
            callData: encodeCall(GatingContractAbi, 'isStickyRole', [DEFAULT_ADMIN_ROLE, adminAddress])
          }
        ];

        const results = await batchedEthCalls(chainId, client as RpcClient, calls);

        const hasRole = results[0].success
          ? decodeCallResult<boolean>(GatingContractAbi, 'hasRole', results[0].data)
          : false;

        const isSticky = results[1].success
          ? decodeCallResult<boolean>(GatingContractAbi, 'isStickyRole', results[1].data)
          : false;

        setHasAdminRole(hasRole);
        setIsStickyRole(isSticky);
      } catch (err) {
        console.error('Error checking admin status:', err);
        setHasAdminRole(false);
        setIsStickyRole(false);
      } finally {
        setIsCheckingSticky(false);
      }
    };

    const debounce = setTimeout(checkAdminStatus, 300);
    return () => clearTimeout(debounce);
  }, [adminAddress, client, chainId, gatingData.gatingContractAddress]);

  useEffect(() => {
    const timeoutId = window.setTimeout(() => {
      (window as any).explorer?.initControls();
    }, 100);

    return () => window.clearTimeout(timeoutId);
  }, [activeSection]);

  const formatTokenAmount = (amount: string, decimals: number): bigint => {
    const parts = amount.split('.');
    const whole = BigInt(parts[0] || '0');
    let fraction = 0n;
    if (parts[1]) {
      const fractionStr = parts[1].slice(0, decimals).padEnd(decimals, '0');
      fraction = BigInt(fractionStr);
    }
    return whole * BigInt(10 ** decimals) + fraction;
  };

  const handleMint = async () => {
    if (!isAddress(mintAddress)) {
      setErrorModal('Please enter a valid address');
      return;
    }

    const amount = formatTokenAmount(mintAmount, gatingData.tokenDecimals);
    if (amount <= 0n) {
      setErrorModal('Please enter a valid amount');
      return;
    }

    setSuccessMessage(null);
    mintRequest.writeContractAsync({
      address: gatingData.gatingContractAddress as `0x${string}`,
      account: walletAddress,
      abi: GatingContractAbi,
      chain: chain,
      functionName: 'mint',
      args: [mintAddress as `0x${string}`, amount],
    }).then(() => {
      setSuccessMessage(`Successfully minted ${mintAmount} ${gatingData.tokenSymbol} to ${mintAddress}`);
      setMintAddress('');
      setMintAmount('1');
      onSuccess();
    }).catch((error) => {
      setErrorModal(error.message);
    });
  };

  const handleGrantAdmin = async () => {
    if (!isAddress(adminAddress)) {
      setErrorModal('Please enter a valid address');
      return;
    }

    setSuccessMessage(null);
    grantAdminRequest.writeContractAsync({
      address: gatingData.gatingContractAddress as `0x${string}`,
      account: walletAddress,
      abi: GatingContractAbi,
      chain: chain,
      functionName: 'grantRole',
      args: [DEFAULT_ADMIN_ROLE as `0x${string}`, adminAddress as `0x${string}`],
    }).then(() => {
      setSuccessMessage(`Successfully granted admin role to ${adminAddress}`);
      setAdminAddress('');
      onSuccess();
    }).catch((error) => {
      setErrorModal(error.message);
    });
  };

  const handleRevokeAdmin = async () => {
    if (!isAddress(adminAddress)) {
      setErrorModal('Please enter a valid address');
      return;
    }

    if (isStickyRole) {
      setErrorModal('Cannot revoke a sticky admin role');
      return;
    }

    setSuccessMessage(null);
    revokeAdminRequest.writeContractAsync({
      address: gatingData.gatingContractAddress as `0x${string}`,
      account: walletAddress,
      abi: GatingContractAbi,
      chain: chain,
      functionName: 'revokeRole',
      args: [DEFAULT_ADMIN_ROLE as `0x${string}`, adminAddress as `0x${string}`],
    }).then(() => {
      setSuccessMessage(`Successfully revoked admin role from ${adminAddress}`);
      setAdminAddress('');
      onSuccess();
    }).catch((error) => {
      setErrorModal(error.message);
    });
  };

  const handleSetConfig = async () => {
    setSuccessMessage(null);
    setConfigRequest.writeContractAsync({
      address: gatingData.gatingContractAddress as `0x${string}`,
      account: walletAddress,
      abi: GatingContractAbi,
      chain: chain,
      functionName: 'setDepositGateConfig',
      args: [selectedDepositType, configBlocked, configNoToken],
    }).then(() => {
      setSuccessMessage(`Successfully updated config for ${DEPOSIT_TYPE_LABELS[selectedDepositType]}`);
      onSuccess();
    }).catch((error) => {
      setErrorModal(error.message);
    });
  };

  const isPending = mintRequest.isPending || grantAdminRequest.isPending ||
                    revokeAdminRequest.isPending || setConfigRequest.isPending;

  return (
    <Modal show={true} onHide={onClose} size="lg" className="gating-manage-modal">
      <Modal.Header closeButton>
        <Modal.Title>
          <i className="fa fa-cog me-2"></i>
          Manage Gating Contract
        </Modal.Title>
      </Modal.Header>
      <Modal.Body>
        {/* Contract Info */}
        <div className="mb-3 p-2 contract-info rounded">
          <div className="d-flex justify-content-between flex-wrap gap-2">
            <small>
              <strong>Contract:</strong>{' '}
              <span className="font-monospace">{gatingData.gatingContractAddress}</span>
            </small>
            <small>
              <strong>Token:</strong> {gatingData.tokenName} ({gatingData.tokenSymbol})
            </small>
          </div>
        </div>

        {/* Section Tabs */}
        <ul className="nav nav-tabs mb-3">
          <li className="nav-item">
            <button
              className={`nav-link ${activeSection === 'mint' ? 'active' : ''}`}
              onClick={() => setActiveSection('mint')}
            >
              <i className="fa fa-coins me-1"></i> Mint Tokens
            </button>
          </li>
          <li className="nav-item">
            <button
              className={`nav-link ${activeSection === 'admin' ? 'active' : ''}`}
              onClick={() => setActiveSection('admin')}
            >
              <i className="fa fa-user-shield me-1"></i> Manage Admins
            </button>
          </li>
          <li className="nav-item">
            <button
              className={`nav-link ${activeSection === 'config' ? 'active' : ''}`}
              onClick={() => setActiveSection('config')}
            >
              <i className="fa fa-sliders me-1"></i> Deposit Config
            </button>
          </li>
        </ul>

        {/* Success Message */}
        {successMessage && (
          <div className="alert alert-success mb-3">
            <i className="fa fa-check-circle me-2"></i>
            {successMessage}
          </div>
        )}

        {/* Mint Tokens Section */}
        {activeSection === 'mint' && (
          <div>
            <p className="text-muted">
              Mint deposit tokens to allow addresses to make deposits.
            </p>
            <div className="mb-3">
              <label className="form-label">Recipient Address</label>
              <input
                type="text"
                className={`form-control font-monospace ${mintAddress && !isAddress(mintAddress) ? 'is-invalid' : ''}`}
                placeholder="0x..."
                value={mintAddress}
                onChange={(e) => setMintAddress(e.target.value)}
              />
              {mintAddress && !isAddress(mintAddress) && (
                <div className="invalid-feedback">Please enter a valid Ethereum address</div>
              )}
            </div>
            <div className="mb-3">
              <label className="form-label">Amount ({gatingData.tokenSymbol})</label>
              <input
                type="number"
                className="form-control"
                placeholder="1"
                min="0"
                step="1"
                value={mintAmount}
                onChange={(e) => setMintAmount(e.target.value)}
              />
            </div>
            <button
              className="btn btn-primary"
              onClick={handleMint}
              disabled={!isAddress(mintAddress) || mintRequest.isPending}
            >
              {mintRequest.isPending ? (
                <span><span className="spinner-border spinner-border-sm me-1"></span> Minting...</span>
              ) : (
                <span><i className="fa fa-coins me-1"></i> Mint Tokens</span>
              )}
            </button>
          </div>
        )}

        {/* Manage Admins Section */}
        {activeSection === 'admin' && (
          <div>
            <p className="text-muted">
              Grant or revoke admin roles. Admins can mint tokens and configure deposit gating.
            </p>
            <div className="mb-3">
              <label className="form-label">Address</label>
              <input
                type="text"
                className={`form-control font-monospace ${adminAddress && !isAddress(adminAddress) ? 'is-invalid' : ''}`}
                placeholder="0x..."
                value={adminAddress}
                onChange={(e) => setAdminAddress(e.target.value)}
              />
              {adminAddress && !isAddress(adminAddress) && (
                <div className="invalid-feedback">Please enter a valid Ethereum address</div>
              )}
            </div>

            {adminAddress && isAddress(adminAddress) && (
              <div className="mb-3 p-2 status-box rounded">
                {isCheckingSticky ? (
                  <span><span className="spinner-border spinner-border-sm me-1"></span> Checking status...</span>
                ) : (
                  <div className="d-flex gap-3">
                    <span>
                      <strong>Has Admin Role:</strong>{' '}
                      {hasAdminRole ? (
                        <span className="text-success">Yes</span>
                      ) : (
                        <span className="text-muted">No</span>
                      )}
                    </span>
                    {hasAdminRole && (
                      <span>
                        <strong>Sticky:</strong>{' '}
                        {isStickyRole ? (
                          <span className="text-warning" data-bs-toggle="tooltip" title="Sticky roles cannot be revoked">
                            Yes <i className="fa fa-lock"></i>
                          </span>
                        ) : (
                          <span className="text-muted">No</span>
                        )}
                      </span>
                    )}
                  </div>
                )}
              </div>
            )}

            <div className="d-flex gap-2">
              <button
                className="btn btn-success"
                onClick={handleGrantAdmin}
                disabled={!isAddress(adminAddress) || grantAdminRequest.isPending || hasAdminRole}
              >
                {grantAdminRequest.isPending ? (
                  <span><span className="spinner-border spinner-border-sm me-1"></span> Granting...</span>
                ) : (
                  <span><i className="fa fa-user-plus me-1"></i> Grant Admin</span>
                )}
              </button>
              <button
                className="btn btn-danger"
                onClick={handleRevokeAdmin}
                disabled={!isAddress(adminAddress) || revokeAdminRequest.isPending || !hasAdminRole || isStickyRole}
              >
                {revokeAdminRequest.isPending ? (
                  <span><span className="spinner-border spinner-border-sm me-1"></span> Revoking...</span>
                ) : (
                  <span><i className="fa fa-user-minus me-1"></i> Revoke Admin</span>
                )}
              </button>
            </div>
            {hasAdminRole && isStickyRole && (
              <div className="text-warning mt-2">
                <i className="fa fa-exclamation-triangle me-1"></i>
                This admin role is sticky and cannot be revoked.
              </div>
            )}
          </div>
        )}

        {/* Deposit Config Section */}
        {activeSection === 'config' && (
          <div>
            <p className="text-muted">
              Configure which deposit types are allowed and whether they require burning a token.
            </p>
            <div className="mb-3">
              <label className="form-label">Deposit Type</label>
              <select
                className="form-select"
                value={selectedDepositType}
                onChange={(e) => setSelectedDepositType(Number(e.target.value))}
              >
                {Object.entries(DEPOSIT_TYPES).map(([key, value]) => (
                  <option key={key} value={value}>
                    {DEPOSIT_TYPE_LABELS[value]}
                  </option>
                ))}
              </select>
            </div>

            <div className="mb-3">
              <div className="form-check form-switch">
                <input
                  className="form-check-input"
                  type="checkbox"
                  id="configBlocked"
                  checked={configBlocked}
                  onChange={(e) => setConfigBlocked(e.target.checked)}
                />
                <label className="form-check-label" htmlFor="configBlocked">
                  <strong>Blocked</strong>
                  <span className="text-muted ms-2">- Deposits of this type are not allowed</span>
                </label>
              </div>
            </div>

            <div className="mb-3">
              <div className="form-check form-switch">
                <input
                  className="form-check-input"
                  type="checkbox"
                  id="configNoToken"
                  checked={configNoToken}
                  onChange={(e) => setConfigNoToken(e.target.checked)}
                  disabled={configBlocked}
                />
                <label className="form-check-label" htmlFor="configNoToken">
                  <strong>No Token Required</strong>
                  <span className="text-muted ms-2">- Deposits don't require burning a token</span>
                </label>
              </div>
            </div>

            {/* Config Preview */}
            <div className="mb-3 p-2 status-box rounded">
              <strong>Current Status:</strong>{' '}
              {configBlocked ? (
                <span className="text-danger">
                  <i className="fa fa-ban me-1"></i>Blocked
                </span>
              ) : configNoToken ? (
                <span className="text-success">
                  <i className="fa fa-check-circle me-1"></i>Allowed (no token required)
                </span>
              ) : (
                <span className="text-warning">
                  <i className="fa fa-coins me-1"></i>Allowed (requires token)
                </span>
              )}
            </div>

            <button
              className="btn btn-primary"
              onClick={handleSetConfig}
              disabled={setConfigRequest.isPending}
            >
              {setConfigRequest.isPending ? (
                <span><span className="spinner-border spinner-border-sm me-1"></span> Saving...</span>
              ) : (
                <span><i className="fa fa-save me-1"></i> Save Configuration</span>
              )}
            </button>
          </div>
        )}
      </Modal.Body>
      <Modal.Footer>
        <button className="btn btn-secondary" onClick={onClose} disabled={isPending}>
          Close
        </button>
      </Modal.Footer>

      {/* Error Modal */}
      {errorModal && (
        <Modal show={true} onHide={() => setErrorModal(null)} size="lg">
          <Modal.Header closeButton>
            <Modal.Title>Transaction Failed</Modal.Title>
          </Modal.Header>
          <Modal.Body>
            <pre className="m-0 deposit-error">{errorModal}</pre>
          </Modal.Body>
          <Modal.Footer>
            <button className="btn btn-primary" onClick={() => setErrorModal(null)}>Close</button>
          </Modal.Footer>
        </Modal>
      )}
    </Modal>
  );
};

export default GatingManageModal;
