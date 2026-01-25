import { ContainerType, ByteVectorType, UintNumberType } from "@chainsafe/ssz";
import bls from "@chainsafe/bls/herumi";
import { deriveKeyFromMnemonic, deriveEth2ValidatorKeys } from "@chainsafe/bls-keygen";
import { validateMnemonic } from "@scure/bip39";
import { wordlist } from "@scure/bip39/wordlists/english";

import { IDeposit } from "./DepositsTable";

// SSZ Type Definitions
const DepositMessage = new ContainerType({
  pubkey: new ByteVectorType(48),
  withdrawal_credentials: new ByteVectorType(32),
  amount: new UintNumberType(8),
});

const DepositData = new ContainerType({
  pubkey: new ByteVectorType(48),
  withdrawal_credentials: new ByteVectorType(32),
  amount: new UintNumberType(8),
  signature: new ByteVectorType(96),
});

const ForkData = new ContainerType({
  current_version: new ByteVectorType(4),
  genesis_validators_root: new ByteVectorType(32),
});

const SigningData = new ContainerType({
  object_root: new ByteVectorType(32),
  domain: new ByteVectorType(32),
});

export type CredentialType = '00' | '01' | '02';

export interface WithdrawalCredentialConfig {
  type: CredentialType;
  address?: string; // For 0x01/0x02: ETH address
  // For 0x00: uses derived withdrawal pubkey automatically
}

export interface ValidatorOverride {
  index: number; // Relative index (0-based within the batch)
  amount?: number; // In gwei, optional override
  credentialConfig?: WithdrawalCredentialConfig; // Credential type override
  rawCredentials?: string; // Raw 32-byte hex (only for advanced mode)
}

export interface GeneratorConfig {
  mnemonic: string;
  startIndex: number;
  validatorCount: number;
  defaultAmountGwei: number;
  defaultCredentialConfig: WithdrawalCredentialConfig;
  useRawCredentials?: boolean; // Advanced mode: use raw credentials string
  defaultRawCredentials?: string; // Raw 32-byte hex (0x prefixed) for advanced mode
  overrides: ValidatorOverride[];
}

/**
 * Validate a BIP-39 mnemonic phrase
 */
export function validateMnemonicWords(mnemonic: string): boolean {
  const normalizedMnemonic = mnemonic.trim().toLowerCase().replace(/\s+/g, ' ');
  return validateMnemonic(normalizedMnemonic, wordlist);
}

/**
 * Build withdrawal credentials from type and ETH address
 * @param credType - '01' for execution, '02' for compounding
 * @param address - 20-byte ETH address (0x prefixed)
 */
export function buildWithdrawalCredentialsFromAddress(credType: '01' | '02', address: string): string {
  const cleanAddress = address.startsWith('0x') ? address.slice(2) : address;
  if (cleanAddress.length !== 40) {
    throw new Error("Invalid address length");
  }
  // 1 byte type + 11 zero bytes + 20 byte address = 32 bytes
  return `0x${credType}${'00'.repeat(11)}${cleanAddress.toLowerCase()}`;
}

/**
 * Build 0x00 BLS withdrawal credentials from withdrawal pubkey
 * Format: 0x00 + SHA256(withdrawal_pubkey)[1:32]
 * @param withdrawalPubkey - 48-byte BLS public key
 */
export async function buildBLSWithdrawalCredentials(withdrawalPubkey: Uint8Array): Promise<string> {
  // SHA256 hash of the withdrawal pubkey
  const hashBuffer = await crypto.subtle.digest('SHA-256', withdrawalPubkey.buffer as ArrayBuffer);
  const hashArray = new Uint8Array(hashBuffer);

  // Build credentials: replace first byte with 0x00, keep rest of hash
  const credentials = new Uint8Array(32);
  credentials[0] = 0x00; // BLS_WITHDRAWAL_PREFIX
  credentials.set(hashArray.slice(1, 32), 1); // hash[1:32]

  return '0x' + bytesToHex(credentials);
}

/**
 * Build withdrawal credentials based on configuration
 */
export async function buildWithdrawalCredentials(
  config: WithdrawalCredentialConfig,
  withdrawalKeyBytes?: Uint8Array
): Promise<string> {
  if (config.type === '00') {
    if (!withdrawalKeyBytes) {
      throw new Error("Withdrawal key required for 0x00 credentials");
    }
    // Get withdrawal pubkey from the withdrawal secret key
    const withdrawalSecretKey = bls.SecretKey.fromBytes(withdrawalKeyBytes);
    const withdrawalPubkey = withdrawalSecretKey.toPublicKey().toBytes();
    return buildBLSWithdrawalCredentials(withdrawalPubkey);
  } else {
    if (!config.address) {
      throw new Error("Address required for 0x01/0x02 credentials");
    }
    return buildWithdrawalCredentialsFromAddress(config.type, config.address);
  }
}

/**
 * Validate raw withdrawal credentials (32 bytes hex)
 */
export function validateWithdrawalCredentials(credentials: string): boolean {
  const clean = credentials.startsWith('0x') ? credentials.slice(2) : credentials;
  if (clean.length !== 64) return false;
  return /^[0-9a-fA-F]{64}$/.test(clean);
}

/**
 * Generate deposits from mnemonic and configuration
 */
export async function generateDeposits(
  config: GeneratorConfig,
  genesisForkVersion: string
): Promise<IDeposit[]> {
  // Note: BLS library must be initialized before calling this function
  // The caller (DepositGeneratorModal) handles BLS initialization

  // Validate mnemonic
  const normalizedMnemonic = config.mnemonic.trim().toLowerCase().replace(/\s+/g, ' ');
  if (!validateMnemonicWords(normalizedMnemonic)) {
    throw new Error("Invalid mnemonic phrase");
  }

  // Validate default raw credentials if using advanced mode
  if (config.useRawCredentials && config.defaultRawCredentials) {
    if (!validateWithdrawalCredentials(config.defaultRawCredentials)) {
      throw new Error("Invalid default withdrawal credentials");
    }
  }

  // Derive master key from mnemonic
  const masterKey = deriveKeyFromMnemonic(normalizedMnemonic);

  // Compute signing domain
  const signingDomain = computeSigningDomain(genesisForkVersion);

  const deposits: IDeposit[] = [];

  for (let i = 0; i < config.validatorCount; i++) {
    const validatorIndex = config.startIndex + i;
    const override = config.overrides.find(o => o.index === i);

    const amountGwei = override?.amount ?? config.defaultAmountGwei;

    // Derive validator keys (needed for both signing and potentially 0x00 credentials)
    const keys = deriveEth2ValidatorKeys(masterKey, validatorIndex);

    // Determine withdrawal credentials
    let withdrawalCredentialsHex: string;

    if (override?.rawCredentials) {
      // Override with raw credentials
      if (!validateWithdrawalCredentials(override.rawCredentials)) {
        throw new Error(`Invalid raw withdrawal credentials for validator at index ${i}`);
      }
      withdrawalCredentialsHex = override.rawCredentials.startsWith('0x')
        ? override.rawCredentials
        : `0x${override.rawCredentials}`;
    } else if (override?.credentialConfig) {
      // Override with credential config (type + address or 0x00)
      withdrawalCredentialsHex = await buildWithdrawalCredentials(
        override.credentialConfig,
        keys.withdrawal
      );
    } else if (config.useRawCredentials && config.defaultRawCredentials) {
      // Default raw credentials (advanced mode)
      withdrawalCredentialsHex = config.defaultRawCredentials.startsWith('0x')
        ? config.defaultRawCredentials
        : `0x${config.defaultRawCredentials}`;
    } else {
      // Default credential config
      withdrawalCredentialsHex = await buildWithdrawalCredentials(
        config.defaultCredentialConfig,
        keys.withdrawal
      );
    }

    const deposit = generateSingleDeposit(
      keys,
      amountGwei,
      withdrawalCredentialsHex,
      signingDomain,
      genesisForkVersion
    );

    deposits.push(deposit);
  }

  return deposits;
}

interface ValidatorKeys {
  signing: Uint8Array;
  withdrawal: Uint8Array;
}

function generateSingleDeposit(
  keys: ValidatorKeys,
  amountGwei: number,
  withdrawalCredentialsHex: string,
  signingDomain: Uint8Array,
  forkVersion: string
): IDeposit {
  // Convert to BLS secret key and get public key
  const secretKey = bls.SecretKey.fromBytes(keys.signing);
  const pubkey = secretKey.toPublicKey().toBytes();

  // Parse withdrawal credentials
  const withdrawalCredentials = hexToBytes(withdrawalCredentialsHex);

  // Create deposit message
  const depositMessageData = {
    pubkey,
    withdrawal_credentials: withdrawalCredentials,
    amount: amountGwei,
  };

  // Compute deposit message root
  const depositMessageRoot = DepositMessage.hashTreeRoot(depositMessageData);

  // Create signing data and compute signing root
  const signingData = {
    object_root: depositMessageRoot,
    domain: signingDomain,
  };
  const signingRoot = SigningData.hashTreeRoot(signingData);

  // Sign the deposit
  const signature = secretKey.sign(signingRoot).toBytes();

  // Compute deposit data root (includes signature)
  const depositDataData = {
    pubkey,
    withdrawal_credentials: withdrawalCredentials,
    amount: amountGwei,
    signature,
  };
  const depositDataRoot = DepositData.hashTreeRoot(depositDataData);

  return {
    pubkey: bytesToHex(pubkey),
    withdrawal_credentials: bytesToHex(withdrawalCredentials),
    amount: amountGwei,
    signature: bytesToHex(signature),
    deposit_message_root: bytesToHex(depositMessageRoot),
    deposit_data_root: bytesToHex(depositDataRoot),
    fork_version: forkVersion.startsWith('0x') ? forkVersion.slice(2) : forkVersion,
    network_name: "devnet",
    deposit_cli_version: "dora-generator",
    validity: true, // We just generated and signed it, so it's valid
  };
}

function computeSigningDomain(genesisForkVersion: string): Uint8Array {
  const forkVersionBytes = hexToBytes(genesisForkVersion);

  const forkData = {
    current_version: forkVersionBytes,
    genesis_validators_root: new Uint8Array(32), // Zero hash for deposits
  };
  const forkDataRoot = ForkData.hashTreeRoot(forkData);

  // DOMAIN_DEPOSIT = 0x03000000
  const signingDomain = new Uint8Array(32);
  signingDomain.set([0x03, 0x00, 0x00, 0x00]);
  signingDomain.set(forkDataRoot.slice(0, 28), 4);

  return signingDomain;
}

function hexToBytes(hex: string): Uint8Array {
  const clean = hex.startsWith('0x') ? hex.slice(2) : hex;
  const bytes = new Uint8Array(clean.length / 2);
  for (let i = 0; i < bytes.length; i++) {
    bytes[i] = parseInt(clean.slice(i * 2, i * 2 + 2), 16);
  }
  return bytes;
}

function bytesToHex(bytes: Uint8Array): string {
  return Array.from(bytes).map(b => b.toString(16).padStart(2, '0')).join('');
}

/**
 * Convert ETH amount to gwei
 */
export function ethToGwei(eth: number): number {
  return Math.floor(eth * 1e9);
}

/**
 * Convert gwei amount to ETH
 */
export function gweiToEth(gwei: number): number {
  return gwei / 1e9;
}
