import { useEffect, useRef, useState, useCallback } from 'react';
import { usePublicClient } from 'wagmi';
import {
  GatingContractAbi,
  GATING_CONTRACT_SLOT,
  DEFAULT_ADMIN_ROLE,
  DEPOSIT_TYPES,
  GatingContractData,
  DepositGateConfig
} from '../components/SubmitDepositsForm/GatingContract';
import {
  queuedGetStorageAt,
  batchedEthCalls,
  queuedEthCall,
  encodeCall,
  decodeCallResult,
  RpcClient
} from '../utils/RpcQueue';

interface StaticCacheEntry {
  data: Omit<GatingContractData, 'tokenBalance'> | null;
  timestamp: number;
}

interface BalanceCacheEntry {
  balance: bigint;
  timestamp: number;
}

// Cache durations
const STATIC_CACHE_DURATION = 10 * 60 * 1000; // 10 minutes for static data
const BALANCE_CACHE_DURATION = 15000; // 15 seconds for balance
const BALANCE_POLL_INTERVAL = 30000; // 30 seconds polling for balance

// Global caches shared across all component instances
const staticCache = new Map<string, StaticCacheEntry>();
const balanceCache = new Map<string, BalanceCacheEntry>();
const fetchingStatic = new Set<string>();
const fetchingBalance = new Set<string>();

export const useGatingContract = (
  depositContract: string,
  walletAddress: string | undefined,
  chainId: number | undefined
) => {
  const [forceUpdate, setForceUpdate] = useState(0);
  const [isVisible, setIsVisible] = useState(true);
  const [error, setError] = useState<Error | null>(null);
  const balanceIntervalRef = useRef<NodeJS.Timeout | null>(null);

  // Cache keys
  const staticCacheKey = `${depositContract}:${chainId}`;
  const balanceCacheKey = `${depositContract}:${chainId}:${walletAddress || 'none'}`;

  const client = usePublicClient({ chainId });

  // Visibility detection
  useEffect(() => {
    const handleVisibilityChange = () => {
      setIsVisible(document.visibilityState === 'visible');
    };

    document.addEventListener('visibilitychange', handleVisibilityChange);

    return () => {
      document.removeEventListener('visibilitychange', handleVisibilityChange);
    };
  }, []);

  // Fetch static data (admin status, configs, token metadata) - cached for 10 minutes
  const fetchStaticData = useCallback(async (): Promise<Omit<GatingContractData, 'tokenBalance'> | null> => {
    if (!client || !depositContract || !chainId) return null;

    // Check if already fetching
    if (fetchingStatic.has(staticCacheKey)) {
      return staticCache.get(staticCacheKey)?.data || null;
    }

    // Check cache
    const cached = staticCache.get(staticCacheKey);
    if (cached && Date.now() - cached.timestamp < STATIC_CACHE_DURATION) {
      return cached.data;
    }

    fetchingStatic.add(staticCacheKey);

    try {
      // Read the gating contract address from storage slot 0x41
      const slotHex = GATING_CONTRACT_SLOT.startsWith('0x') ? GATING_CONTRACT_SLOT.slice(2) : GATING_CONTRACT_SLOT;
      const storageSlotPadded = ('0x' + slotHex.padStart(64, '0')) as `0x${string}`;

      const storageResult = await queuedGetStorageAt(
        chainId,
        client as RpcClient,
        depositContract as `0x${string}`,
        storageSlotPadded
      );

      if (!storageResult || storageResult === '0x0000000000000000000000000000000000000000000000000000000000000000') {
        // No gating contract configured
        staticCache.set(staticCacheKey, { data: null, timestamp: Date.now() });
        return null;
      }

      // Extract address from storage data (last 20 bytes = 40 hex chars)
      const gatingContractAddress = ('0x' + storageResult.slice(-40)) as `0x${string}`;

      // Build calls for static data
      const configTypes = [
        DEPOSIT_TYPES.BLS,
        DEPOSIT_TYPES.EXECUTION,
        DEPOSIT_TYPES.COMPOUNDING,
        DEPOSIT_TYPES.EPBS,
        DEPOSIT_TYPES.TOPUP
      ];

      const calls: Array<{ target: `0x${string}`; callData: `0x${string}` }> = [
        { target: gatingContractAddress, callData: encodeCall(GatingContractAbi, 'name') },
        { target: gatingContractAddress, callData: encodeCall(GatingContractAbi, 'symbol') },
        { target: gatingContractAddress, callData: encodeCall(GatingContractAbi, 'decimals') },
        { target: gatingContractAddress, callData: encodeCall(GatingContractAbi, 'totalSupply') },
        ...configTypes.map(depositType => ({
          target: gatingContractAddress,
          callData: encodeCall(GatingContractAbi, 'getDepositGateConfig', [depositType]),
        })),
      ];

      // Add admin check if wallet connected
      if (walletAddress) {
        calls.push({
          target: gatingContractAddress,
          callData: encodeCall(GatingContractAbi, 'hasRole', [DEFAULT_ADMIN_ROLE, walletAddress])
        });
      }

      const results = await batchedEthCalls(chainId, client as RpcClient, calls);

      // Parse results
      const tokenName = results[0].success ? decodeCallResult<string>(GatingContractAbi, 'name', results[0].data) : 'Unknown';
      const tokenSymbol = results[1].success ? decodeCallResult<string>(GatingContractAbi, 'symbol', results[1].data) : '???';
      const tokenDecimals = results[2].success ? Number(decodeCallResult<number>(GatingContractAbi, 'decimals', results[2].data)) : 18;
      const totalSupply = results[3].success ? decodeCallResult<bigint>(GatingContractAbi, 'totalSupply', results[3].data) : 0n;

      // Parse deposit configs
      const depositConfigs = new Map<number, DepositGateConfig>();
      for (let i = 0; i < configTypes.length; i++) {
        const result = results[4 + i];
        if (result.success) {
          try {
            const [blocked, noToken] = decodeCallResult<readonly [boolean, boolean]>(GatingContractAbi, 'getDepositGateConfig', result.data);
            depositConfigs.set(configTypes[i], { blocked, noToken });
          } catch {
            depositConfigs.set(configTypes[i], { blocked: true, noToken: false });
          }
        } else {
          depositConfigs.set(configTypes[i], { blocked: true, noToken: false });
        }
      }

      // Parse admin status
      let isAdmin = false;
      if (walletAddress) {
        const adminResult = results[4 + configTypes.length];
        if (adminResult?.success) {
          try {
            isAdmin = decodeCallResult<boolean>(GatingContractAbi, 'hasRole', adminResult.data);
          } catch {
            isAdmin = false;
          }
        }
      }

      const staticData = {
        gatingContractAddress,
        isAdmin,
        tokenName,
        tokenSymbol,
        tokenDecimals,
        totalSupply,
        depositConfigs,
      };

      staticCache.set(staticCacheKey, { data: staticData, timestamp: Date.now() });
      return staticData;

    } catch (err) {
      console.error('Error fetching static gating data:', err);
      staticCache.set(staticCacheKey, { data: null, timestamp: Date.now() });
      return null;
    } finally {
      fetchingStatic.delete(staticCacheKey);
    }
  }, [staticCacheKey, client, depositContract, chainId, walletAddress]);

  // Fetch token balance only - refreshed more frequently
  const fetchBalance = useCallback(async (gatingContractAddress: `0x${string}`): Promise<bigint> => {
    if (!client || !chainId || !walletAddress) return 0n;

    // Check if already fetching
    if (fetchingBalance.has(balanceCacheKey)) {
      return balanceCache.get(balanceCacheKey)?.balance ?? 0n;
    }

    // Check cache
    const cached = balanceCache.get(balanceCacheKey);
    if (cached && Date.now() - cached.timestamp < BALANCE_CACHE_DURATION) {
      return cached.balance;
    }

    fetchingBalance.add(balanceCacheKey);

    try {
      const callData = encodeCall(GatingContractAbi, 'balanceOf', [walletAddress]);
      const result = await queuedEthCall(chainId, client as RpcClient, gatingContractAddress, callData);
      const balance = decodeCallResult<bigint>(GatingContractAbi, 'balanceOf', result);

      balanceCache.set(balanceCacheKey, { balance, timestamp: Date.now() });
      return balance;
    } catch (err) {
      console.error('Error fetching token balance:', err);
      return balanceCache.get(balanceCacheKey)?.balance ?? 0n;
    } finally {
      fetchingBalance.delete(balanceCacheKey);
    }
  }, [balanceCacheKey, client, chainId, walletAddress]);

  // Combined fetch that returns full GatingContractData
  const fetchGatingData = useCallback(async (): Promise<GatingContractData | null> => {
    setError(null);

    try {
      const staticData = await fetchStaticData();
      if (!staticData) {
        setForceUpdate(prev => prev + 1);
        return null;
      }

      let tokenBalance = 0n;
      if (walletAddress && staticData.gatingContractAddress) {
        tokenBalance = await fetchBalance(staticData.gatingContractAddress as `0x${string}`);
      }

      const gatingData: GatingContractData = {
        ...staticData,
        tokenBalance,
      };

      setForceUpdate(prev => prev + 1);
      return gatingData;

    } catch (err) {
      console.error('Error fetching gating contract data:', err);
      setError(err as Error);
      setForceUpdate(prev => prev + 1);
      return null;
    }
  }, [fetchStaticData, fetchBalance, walletAddress]);

  // Refresh balance only (for periodic updates)
  const refreshBalanceOnly = useCallback(async () => {
    const staticData = staticCache.get(staticCacheKey)?.data;
    if (!staticData?.gatingContractAddress || !walletAddress) return;

    // Clear balance cache to force refresh
    balanceCache.delete(balanceCacheKey);
    await fetchBalance(staticData.gatingContractAddress as `0x${string}`);
    setForceUpdate(prev => prev + 1);
  }, [staticCacheKey, balanceCacheKey, fetchBalance, walletAddress]);

  // Initial fetch on mount
  useEffect(() => {
    fetchGatingData();
  }, [fetchGatingData]);

  // Periodic balance refresh (every 30 seconds when visible)
  useEffect(() => {
    if (!isVisible) return;

    balanceIntervalRef.current = setInterval(() => {
      refreshBalanceOnly();
    }, BALANCE_POLL_INTERVAL);

    return () => {
      if (balanceIntervalRef.current) {
        clearInterval(balanceIntervalRef.current);
      }
    };
  }, [refreshBalanceOnly, isVisible]);

  // Refetch when wallet address changes
  useEffect(() => {
    // Clear balance cache for this wallet
    balanceCache.delete(balanceCacheKey);
    // Static data includes admin status which depends on wallet, so clear if wallet changes
    staticCache.delete(staticCacheKey);
    fetchGatingData();
  }, [walletAddress, chainId]);

  // Get combined data from caches
  const getGatingData = useCallback((): GatingContractData | null => {
    const staticData = staticCache.get(staticCacheKey)?.data;
    if (!staticData) return null;

    const balance = balanceCache.get(balanceCacheKey)?.balance ?? 0n;

    return {
      ...staticData,
      tokenBalance: balance,
    };
  }, [staticCacheKey, balanceCacheKey, forceUpdate]);

  // Force refetch everything (used after admin actions)
  const refetch = useCallback(() => {
    staticCache.delete(staticCacheKey);
    balanceCache.delete(balanceCacheKey);
    return fetchGatingData();
  }, [staticCacheKey, balanceCacheKey, fetchGatingData]);

  return {
    gatingData: getGatingData(),
    refetch,
    isLoading: fetchingStatic.has(staticCacheKey) && !staticCache.has(staticCacheKey),
    error,
  };
};
