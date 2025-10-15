import { useEffect, useRef, useState, useCallback } from 'react';
import { useStorageAt, useBlockNumber, usePublicClient } from 'wagmi';

interface QueueData {
  queueLength: bigint;
  lastFetch: number;
  isLoading: boolean;
  error: Error | null;
}

interface LogData {
  lastBlock: bigint;
  logCount: {[block: string]: number};
}

interface CacheEntry<T> {
  data: T;
  timestamp: number;
}

const CACHE_DURATION = 15000; // 15 seconds cache
const LOG_LOOKBACK_RANGE = 10;

// Global cache shared across all component instances
const queueCache = new Map<string, CacheEntry<QueueData>>();
const logCache = new Map<string, CacheEntry<LogData>>();
const fetchingContracts = new Set<string>();

export const useQueueDataCache = (contractAddress: string, chainId?: number) => {
  const [forceUpdate, setForceUpdate] = useState(0);
  const [isVisible, setIsVisible] = useState(true);
  const intervalRef = useRef<NodeJS.Timeout | null>(null);
  
  const cacheKey = chainId ? `${contractAddress}:${chainId}` : contractAddress;
  
  const blockNumber = useBlockNumber({
    watch: {
      enabled: true,
      pollingInterval: 10000,
    },
    chainId,
  });
  
  const client = usePublicClient({
    chainId,
  });
  
  const storageCall = useStorageAt({
    address: contractAddress as `0x${string}`,
    slot: "0x00",
    chainId,
    query: {
      enabled: false, // We'll manually control when to fetch
    }
  });

  // Visibility detection
  useEffect(() => {
    const handleVisibilityChange = () => {
      setIsVisible(document.visibilityState === 'visible');
    };
    
    const observer = new IntersectionObserver(
      ([entry]) => {
        setIsVisible(entry.isIntersecting && document.visibilityState === 'visible');
      },
      { threshold: 0.1 }
    );
    
    document.addEventListener('visibilitychange', handleVisibilityChange);
    
    return () => {
      document.removeEventListener('visibilitychange', handleVisibilityChange);
      observer.disconnect();
    };
  }, []);

  const fetchQueueData = useCallback(async () => {
    // Check if already fetching
    if (fetchingContracts.has(cacheKey)) return;
    
    // Check cache
    const cached = queueCache.get(cacheKey);
    if (cached && Date.now() - cached.timestamp < CACHE_DURATION) {
      return cached.data;
    }
    
    fetchingContracts.add(cacheKey);
    
    try {
      const result = await storageCall.refetch();
      
      if (result.data) {
        const queueLength = BigInt(result.data as string);
        const queueData: QueueData = {
          queueLength,
          lastFetch: Date.now(),
          isLoading: false,
          error: null,
        };
        
        queueCache.set(cacheKey, {
          data: queueData,
          timestamp: Date.now(),
        });
        
        setForceUpdate(prev => prev + 1);
        return queueData;
      }
    } catch (error) {
      const queueData: QueueData = {
        queueLength: 0n,
        lastFetch: Date.now(),
        isLoading: false,
        error: error as Error,
      };
      
      queueCache.set(cacheKey, {
        data: queueData,
        timestamp: Date.now(),
      });
      
      setForceUpdate(prev => prev + 1);
      return queueData;
    } finally {
      fetchingContracts.delete(cacheKey);
    }
  }, [cacheKey, storageCall]);

  const fetchLogData = useCallback(async () => {
    if (!client || !blockNumber.data) return;
    
    // Check cache
    const cached = logCache.get(cacheKey);
    if (cached && cached.data.lastBlock >= blockNumber.data) {
      return cached.data;
    }
    
    let fromBlock = blockNumber.data - BigInt(LOG_LOOKBACK_RANGE);
    if (fromBlock < 0n) fromBlock = 0n;
    
    try {
      const logs = await client.request({
        method: 'eth_getLogs',
        params: [{
          address: contractAddress as `0x${string}`,
          fromBlock: `0x${fromBlock.toString(16)}`,
        }],
      });
      
      let logData: LogData = {
        lastBlock: blockNumber.data,
        logCount: {},
      };
      
      (logs as any[]).forEach((log) => {
        let blockNum = BigInt(log.blockNumber);
        let blockStr = blockNum.toString();
        if (!logData.logCount[blockStr]) 
          logData.logCount[blockStr] = 0;
        logData.logCount[blockStr]++;
      });
      
      logCache.set(cacheKey, {
        data: logData,
        timestamp: Date.now(),
      });
      
      setForceUpdate(prev => prev + 1);
      return logData;
    } catch (error) {
      console.error('Error fetching logs:', error);
    }
  }, [cacheKey, client, blockNumber.data, contractAddress]);

  // Auto-fetch queue data on mount and periodically
  useEffect(() => {
    fetchQueueData();
    
    if (!isVisible) return;
    
    intervalRef.current = setInterval(() => {
      fetchQueueData();
    }, 20000); // 20 seconds for queue data polling
    
    return () => {
      if (intervalRef.current) {
        clearInterval(intervalRef.current);
      }
    };
  }, [fetchQueueData, isVisible]);

  // Fetch log data when block changes
  useEffect(() => {
    if (blockNumber.data) {
      fetchLogData();
    }
  }, [blockNumber.data, fetchLogData]);

  const getQueueData = useCallback((): QueueData | null => {
    const cached = queueCache.get(cacheKey);
    if (cached) return cached.data;
    
    // Return loading state if we don't have cached data
    if (fetchingContracts.has(cacheKey)) {
      return {
        queueLength: 0n,
        lastFetch: 0,
        isLoading: true,
        error: null,
      };
    }
    
    return null;
  }, [cacheKey, forceUpdate]);

  const getLogData = useCallback((): LogData | null => {
    const cached = logCache.get(cacheKey);
    return cached ? cached.data : null;
  }, [cacheKey, forceUpdate]);

  const refetch = useCallback(() => {
    // Clear cache to force refetch
    queueCache.delete(cacheKey);
    return fetchQueueData();
  }, [cacheKey, fetchQueueData]);

  return {
    queueData: getQueueData(),
    logData: getLogData(),
    refetch,
    isLoading: fetchingContracts.has(cacheKey) && !queueCache.has(cacheKey),
  };
};