import React from 'react';
import {
  GatingContractData,
  DEPOSIT_TYPE_LABELS
} from './GatingContract';

interface IGatingStatusBannerProps {
  gatingData: GatingContractData | null;
  depositType?: number;
  showDepositStatus?: boolean;
  isLoading?: boolean;
  onManageClick?: () => void;
}

export const GatingStatusBanner: React.FC<IGatingStatusBannerProps> = (props) => {
  const { gatingData, depositType, showDepositStatus = true, isLoading = false, onManageClick } = props;

  // Format as whole tokens (deposits possible), rounded down
  const formatDepositTokens = (balance: bigint, decimals: number): bigint => {
    const divisor = BigInt(10 ** decimals);
    return balance / divisor; // Integer division rounds down
  };

  // Show loading indicator
  if (isLoading) {
    return (
      <div className="gating-status-banner alert alert-secondary mb-3">
        <div className="d-flex align-items-center gap-2">
          <div className="spinner-border spinner-border-sm" role="status"></div>
          <span>Loading gating contract status...</span>
        </div>
      </div>
    );
  }

  // Don't render if no gating data
  if (!gatingData) {
    return null;
  }

  // Get config for specific deposit type if provided
  const config = depositType !== undefined ? gatingData.depositConfigs.get(depositType) : null;

  // Legacy gating contract: simpler UI (no admin, no per-type configs)
  if (gatingData.isLegacy) {
    return (
      <div className="gating-status-banner alert alert-info mb-3">
        <div className="d-flex justify-content-between align-items-center flex-wrap gap-2">
          <div className="d-flex align-items-center gap-3">
            <div>
              <i className="fa fa-shield-halved me-2"></i>
              <strong>Gated Deposits Active</strong>
              <span className="ms-2 text-muted" style={{ fontSize: '0.85em' }}>(legacy)</span>
            </div>
          </div>
          <div className="d-flex align-items-center gap-3">
            <span className="token-balance">
              <i className="fa fa-coins me-1"></i>
              {formatDepositTokens(gatingData.tokenBalance, gatingData.tokenDecimals).toString()} deposits possible
            </span>
          </div>
        </div>
        {showDepositStatus && formatDepositTokens(gatingData.tokenBalance, gatingData.tokenDecimals) === 0n && (
          <div className="mt-2 pt-2 border-top">
            <span className="text-warning">
              <i className="fa fa-lock me-1"></i>
              Token required for deposits (insufficient balance)
            </span>
          </div>
        )}
      </div>
    );
  }

  return (
    <div className="gating-status-banner alert alert-info mb-3">
      <div className="d-flex justify-content-between align-items-center flex-wrap gap-2">
        <div className="d-flex align-items-center gap-3">
          <div>
            <i className="fa fa-shield-halved me-2"></i>
            <strong>Gated Deposits Active</strong>
          </div>
          {gatingData.isAdmin && (
            <span className="badge bg-primary admin-badge">
              <i className="fa fa-user-shield me-1"></i>Admin
            </span>
          )}
          {gatingData.isAdmin && onManageClick && (
            <button
              className="btn btn-sm btn-outline-primary"
              onClick={onManageClick}
            >
              <i className="fa fa-cog me-1"></i>
              Manage
            </button>
          )}
        </div>
        <div className="d-flex align-items-center gap-3">
          <span className="token-balance">
            <i className="fa fa-coins me-1"></i>
            {formatDepositTokens(gatingData.tokenBalance, gatingData.tokenDecimals).toString()} deposits possible
          </span>
        </div>
      </div>

      {showDepositStatus && config && depositType !== undefined && (
        <div className="mt-2 pt-2 border-top">
          <strong>{DEPOSIT_TYPE_LABELS[depositType] || `Type ${depositType}`}:</strong>
          {config.blocked ? (
            <span className="ms-2 text-danger">
              <i className="fa fa-ban me-1"></i>Blocked
            </span>
          ) : config.noToken ? (
            <span className="ms-2 text-success">
              <i className="fa fa-check-circle me-1"></i>Allowed (no token required)
            </span>
          ) : formatDepositTokens(gatingData.tokenBalance, gatingData.tokenDecimals) > 0n ? (
            <span className="ms-2 text-success">
              <i className="fa fa-check-circle me-1"></i>Allowed (token will be burned)
            </span>
          ) : (
            <span className="ms-2 text-warning">
              <i className="fa fa-lock me-1"></i>Requires token (insufficient balance)
            </span>
          )}
        </div>
      )}
    </div>
  );
};

interface IGatingDepositTypeStatusProps {
  gatingData: GatingContractData;
  depositTypes: number[];
}

export const GatingDepositTypeStatus: React.FC<IGatingDepositTypeStatusProps> = (props) => {
  const { gatingData, depositTypes } = props;

  // Helper to get whole token count (deposits possible)
  const depositsAvailable = gatingData.tokenBalance / BigInt(10 ** gatingData.tokenDecimals);

  // Legacy contracts have no per-type configs - just check token balance
  if (gatingData.isLegacy) {
    if (depositsAvailable === 0n) {
      return (
        <div className="alert alert-warning mt-2">
          <i className="fa fa-lock me-2"></i>
          <strong>Token required for deposits</strong>
          <p className="mb-0 mt-1">
            You need at least 1 {gatingData.tokenSymbol} token per deposit. Current balance: 0
          </p>
        </div>
      );
    }
    return null;
  }

  const blockedTypes = depositTypes.filter(type => {
    const config = gatingData.depositConfigs.get(type);
    return config?.blocked;
  });

  const tokenRequiredTypes = depositTypes.filter(type => {
    const config = gatingData.depositConfigs.get(type);
    return !config?.blocked && !config?.noToken && depositsAvailable === 0n;
  });

  if (blockedTypes.length === 0 && tokenRequiredTypes.length === 0) {
    return null;
  }

  return (
    <>
      {blockedTypes.length > 0 && (
        <div className="alert alert-danger mt-2">
          <i className="fa fa-ban me-2"></i>
          <strong>Blocked deposit types in your file:</strong>
          <ul className="mb-0 mt-1">
            {blockedTypes.map(type => (
              <li key={type}>{DEPOSIT_TYPE_LABELS[type] || `Type 0x${type.toString(16).padStart(2, '0')}`}</li>
            ))}
          </ul>
        </div>
      )}

      {tokenRequiredTypes.length > 0 && (
        <div className="alert alert-warning mt-2">
          <i className="fa fa-lock me-2"></i>
          <strong>Token required for these deposit types (insufficient balance):</strong>
          <ul className="mb-0 mt-1">
            {tokenRequiredTypes.map(type => (
              <li key={type}>{DEPOSIT_TYPE_LABELS[type] || `Type 0x${type.toString(16).padStart(2, '0')}`}</li>
            ))}
          </ul>
        </div>
      )}
    </>
  );
};

export default GatingStatusBanner;
