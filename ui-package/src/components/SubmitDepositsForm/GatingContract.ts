// Gating Contract ABI for TokenDepositGater
export const GatingContractAbi = [
  // hasRole(bytes32 role, address account) -> bool
  {
    inputs: [
      { internalType: "bytes32", name: "role", type: "bytes32" },
      { internalType: "address", name: "account", type: "address" }
    ],
    name: "hasRole",
    outputs: [{ internalType: "bool", name: "", type: "bool" }],
    stateMutability: "view",
    type: "function"
  },
  // isStickyRole(bytes32 role, address account) -> bool
  {
    inputs: [
      { internalType: "bytes32", name: "role", type: "bytes32" },
      { internalType: "address", name: "account", type: "address" }
    ],
    name: "isStickyRole",
    outputs: [{ internalType: "bool", name: "", type: "bool" }],
    stateMutability: "view",
    type: "function"
  },
  // getDepositGateConfig(uint16 depositType) -> (bool blocked, bool noToken)
  {
    inputs: [{ internalType: "uint16", name: "depositType", type: "uint16" }],
    name: "getDepositGateConfig",
    outputs: [
      { internalType: "bool", name: "blocked", type: "bool" },
      { internalType: "bool", name: "noToken", type: "bool" }
    ],
    stateMutability: "view",
    type: "function"
  },
  // getCustomGater() -> address
  {
    inputs: [],
    name: "getCustomGater",
    outputs: [{ internalType: "address", name: "", type: "address" }],
    stateMutability: "view",
    type: "function"
  },
  // balanceOf(address account) -> uint256
  {
    inputs: [{ internalType: "address", name: "account", type: "address" }],
    name: "balanceOf",
    outputs: [{ internalType: "uint256", name: "", type: "uint256" }],
    stateMutability: "view",
    type: "function"
  },
  // totalSupply() -> uint256
  {
    inputs: [],
    name: "totalSupply",
    outputs: [{ internalType: "uint256", name: "", type: "uint256" }],
    stateMutability: "view",
    type: "function"
  },
  // name() -> string
  {
    inputs: [],
    name: "name",
    outputs: [{ internalType: "string", name: "", type: "string" }],
    stateMutability: "view",
    type: "function"
  },
  // symbol() -> string
  {
    inputs: [],
    name: "symbol",
    outputs: [{ internalType: "string", name: "", type: "string" }],
    stateMutability: "view",
    type: "function"
  },
  // decimals() -> uint8
  {
    inputs: [],
    name: "decimals",
    outputs: [{ internalType: "uint8", name: "", type: "uint8" }],
    stateMutability: "view",
    type: "function"
  },
  // mint(address to, uint256 amount)
  {
    inputs: [
      { internalType: "address", name: "to", type: "address" },
      { internalType: "uint256", name: "amount", type: "uint256" }
    ],
    name: "mint",
    outputs: [],
    stateMutability: "nonpayable",
    type: "function"
  },
  // grantRole(bytes32 role, address account)
  {
    inputs: [
      { internalType: "bytes32", name: "role", type: "bytes32" },
      { internalType: "address", name: "account", type: "address" }
    ],
    name: "grantRole",
    outputs: [],
    stateMutability: "nonpayable",
    type: "function"
  },
  // revokeRole(bytes32 role, address account)
  {
    inputs: [
      { internalType: "bytes32", name: "role", type: "bytes32" },
      { internalType: "address", name: "account", type: "address" }
    ],
    name: "revokeRole",
    outputs: [],
    stateMutability: "nonpayable",
    type: "function"
  },
  // setDepositGateConfig(uint16 depositType, bool blocked, bool noToken)
  {
    inputs: [
      { internalType: "uint16", name: "depositType", type: "uint16" },
      { internalType: "bool", name: "blocked", type: "bool" },
      { internalType: "bool", name: "noToken", type: "bool" }
    ],
    name: "setDepositGateConfig",
    outputs: [],
    stateMutability: "nonpayable",
    type: "function"
  }
] as const;

// Default admin role for the gating contract
export const DEFAULT_ADMIN_ROLE = "0xacce55000000000000000000ffffffffffffffffffffffffffffffffffffffff";

// Storage slot for gating contract address on deposit contract
export const GATING_CONTRACT_SLOT = "0x41";

// Deposit type constants
export const DEPOSIT_TYPES = {
  BLS: 0x00,
  EXECUTION: 0x01,
  COMPOUNDING: 0x02,
  EPBS: 0x03,
  TOPUP: 0xffff
} as const;

// Human-readable labels for deposit types
export const DEPOSIT_TYPE_LABELS: Record<number, string> = {
  [DEPOSIT_TYPES.BLS]: "BLS Withdrawal (0x00)",
  [DEPOSIT_TYPES.EXECUTION]: "Execution Withdrawal (0x01)",
  [DEPOSIT_TYPES.COMPOUNDING]: "Compounding (0x02)",
  [DEPOSIT_TYPES.EPBS]: "ePBS Builder (0x03)",
  [DEPOSIT_TYPES.TOPUP]: "Topup Deposits"
};

// Map withdrawal credential prefix to deposit type
export const PREFIX_TO_DEPOSIT_TYPE: Record<string, number> = {
  "00": DEPOSIT_TYPES.BLS,
  "01": DEPOSIT_TYPES.EXECUTION,
  "02": DEPOSIT_TYPES.COMPOUNDING,
  "03": DEPOSIT_TYPES.EPBS
};

// Deposit gate config interface
export interface DepositGateConfig {
  blocked: boolean;
  noToken: boolean;
}

// Gating contract data interface
export interface GatingContractData {
  gatingContractAddress: string;
  isAdmin: boolean;
  isLegacy: boolean; // Legacy: deposit contract itself is the ERC20 token (no admin, no configs)
  tokenBalance: bigint;
  tokenName: string;
  tokenSymbol: string;
  tokenDecimals: number;
  totalSupply: bigint;
  depositConfigs: Map<number, DepositGateConfig>;
}
