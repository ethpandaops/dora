/**
 * Central RPC request queue with Multicall3 batching support.
 * Uses raw fetch to bypass viem's built-in retry logic.
 */

import { encodeFunctionData, decodeFunctionResult, decodeAbiParameters, Abi } from 'viem';

// Multicall3 contract
export const MULTICALL3_ADDRESS = '0xcA11bde05977b3631167028862bE2a173976CA11' as const;

export const Multicall3Abi = [
  {
    inputs: [
      {
        components: [
          { name: 'target', type: 'address' },
          { name: 'allowFailure', type: 'bool' },
          { name: 'callData', type: 'bytes' }
        ],
        name: 'calls',
        type: 'tuple[]'
      }
    ],
    name: 'aggregate3',
    outputs: [
      {
        components: [
          { name: 'success', type: 'bool' },
          { name: 'returnData', type: 'bytes' }
        ],
        name: 'returnData',
        type: 'tuple[]'
      }
    ],
    stateMutability: 'view',
    type: 'function'
  }
] as const;

// Configuration
const RATE_LIMIT_PAUSE_MS = 2000;
const MAX_RETRIES = 3;
const MIN_INTERVAL_MS = 100;
const BATCH_COLLECT_MS = 50;
const MAX_BATCH_SIZE = 50;

// Types - now requires rpcUrl instead of client
export interface RpcClient {
  transport?: { url?: string };
}

interface QueueItem {
  type: 'single' | 'batch';
  rpcUrl: string;
  method: string;
  params: unknown[];
  resolve: (result: unknown) => void;
  reject: (error: Error) => void;
  retries: number;
  // For batch items
  batchCalls?: Array<{ target: `0x${string}`; callData: `0x${string}` }>;
  batchResolvers?: Array<(result: { success: boolean; data: string }) => void>;
}

interface ChainQueue {
  items: QueueItem[];
  isProcessing: boolean;
  lastRequestTime: number;
  pausedUntil: number;
  multicallAvailable: boolean | null;
  multicallChecked: boolean;
  rpcUrl: string | null;
  // Pending batch collection
  pendingBatchCalls: Array<{ target: `0x${string}`; callData: `0x${string}` }>;
  pendingBatchResolvers: Array<(result: { success: boolean; data: string }) => void>;
  pendingBatchRejecters: Array<(error: Error) => void>;
  batchCollectTimer: ReturnType<typeof setTimeout> | null;
}

const queues = new Map<number, ChainQueue>();

function getQueue(chainId: number): ChainQueue {
  let queue = queues.get(chainId);
  if (!queue) {
    queue = {
      items: [],
      isProcessing: false,
      lastRequestTime: 0,
      pausedUntil: 0,
      multicallAvailable: null,
      multicallChecked: false,
      rpcUrl: null,
      pendingBatchCalls: [],
      pendingBatchResolvers: [],
      pendingBatchRejecters: [],
      batchCollectTimer: null,
    };
    queues.set(chainId, queue);
  }
  return queue;
}

/**
 * Extract RPC URL from viem client
 */
function getRpcUrl(client: RpcClient): string {
  // Try to get URL from transport
  if (client.transport?.url) {
    return client.transport.url;
  }
  // Fallback to common path for wagmi clients
  const c = client as Record<string, unknown>;
  if (c.chain && typeof c.chain === 'object') {
    const chain = c.chain as Record<string, unknown>;
    if (chain.rpcUrls && typeof chain.rpcUrls === 'object') {
      const rpcUrls = chain.rpcUrls as Record<string, { http?: string[] }>;
      if (rpcUrls.default?.http?.[0]) {
        return rpcUrls.default.http[0];
      }
    }
  }
  throw new Error('Could not extract RPC URL from client');
}

/**
 * Raw JSON-RPC request using fetch - no retry logic
 */
async function rawRpcRequest(rpcUrl: string, method: string, params: unknown[]): Promise<unknown> {
  const response = await fetch(rpcUrl, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      jsonrpc: '2.0',
      id: Date.now(),
      method,
      params,
    }),
  });

  if (!response.ok) {
    const error = new Error(`HTTP ${response.status}: ${response.statusText}`) as Error & { status: number };
    error.status = response.status;
    throw error;
  }

  const json = await response.json() as { result?: unknown; error?: { message: string; code: number } };

  if (json.error) {
    throw new Error(json.error.message || 'RPC Error');
  }

  return json.result;
}

function isRateLimitError(err: unknown): boolean {
  if (!err) return false;
  const s = String(err).toLowerCase();
  if (s.includes('429') || s.includes('rate limit') || s.includes('too many')) return true;
  if (typeof err === 'object') {
    const e = err as Record<string, unknown>;
    const code = e.status || e.statusCode || e.code;
    if (code === 429 || (typeof code === 'number' && code >= 400 && code < 500)) return true;
  }
  return false;
}

function sleep(ms: number): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, ms));
}

/**
 * The single queue processor - runs one item at a time
 */
async function processQueue(chainId: number): Promise<void> {
  const queue = getQueue(chainId);

  if (queue.isProcessing) return;
  queue.isProcessing = true;

  while (queue.items.length > 0) {
    // Wait for rate limit pause BEFORE shifting item
    const now = Date.now();
    if (queue.pausedUntil > now) {
      await sleep(queue.pausedUntil - now);
    }

    // Wait for minimum interval
    const elapsed = Date.now() - queue.lastRequestTime;
    if (elapsed < MIN_INTERVAL_MS && queue.lastRequestTime > 0) {
      await sleep(MIN_INTERVAL_MS - elapsed);
    }

    const item = queue.items.shift()!;
    queue.lastRequestTime = Date.now();

    try {
      if (item.type === 'batch' && item.batchCalls && item.batchResolvers) {
        await executeBatch(queue, item);
      } else {
        const result = await rawRpcRequest(item.rpcUrl, item.method, item.params);
        item.resolve(result);
      }
    } catch (err) {
      if (isRateLimitError(err) && item.retries < MAX_RETRIES) {
        // Pause and retry
        queue.pausedUntil = Date.now() + RATE_LIMIT_PAUSE_MS;
        console.log(`RPC: rate limited, pausing ${RATE_LIMIT_PAUSE_MS}ms, retry ${item.retries + 1}/${MAX_RETRIES}`);
        item.retries++;
        queue.items.unshift(item); // Put back at front
      } else {
        // For batch items, resolve all pending resolvers with failure
        if (item.type === 'batch' && item.batchResolvers) {
          console.log(`RPC: batch item failed, resolving ${item.batchResolvers.length} calls with failure`);
          item.batchResolvers.forEach(resolve => {
            resolve({ success: false, data: '0x' });
          });
        } else {
          item.reject(err as Error);
        }
      }
    }
  }

  queue.isProcessing = false;
}

/**
 * Execute a batch of calls via multicall or individually
 */
async function executeBatch(queue: ChainQueue, item: QueueItem): Promise<void> {
  const calls = item.batchCalls!;
  const resolvers = item.batchResolvers!;

  if (queue.multicallAvailable) {
    // Try multicall
    const encodedCalls = calls.map(c => ({
      target: c.target,
      allowFailure: true,
      callData: c.callData,
    }));

    const multicallData = encodeFunctionData({
      abi: Multicall3Abi,
      functionName: 'aggregate3',
      args: [encodedCalls],
    });

    try {
      const result = await rawRpcRequest(item.rpcUrl, 'eth_call', [
        { to: MULTICALL3_ADDRESS, data: multicallData },
        'latest'
      ]) as string;

      const decoded = decodeAbiParameters(
        [{
          name: 'returnData',
          type: 'tuple[]',
          components: [
            { name: 'success', type: 'bool' },
            { name: 'returnData', type: 'bytes' }
          ]
        }],
        result as `0x${string}`
      );

      const results = decoded[0] as Array<{ success: boolean; returnData: string }>;
      resolvers.forEach((resolve, i) => {
        resolve({ success: results[i].success, data: results[i].returnData });
      });
      return;
    } catch (err) {
      if (!isRateLimitError(err)) {
        // Multicall failed for non-rate-limit reason, disable it
        queue.multicallAvailable = false;
      } else {
        throw err; // Let the main loop handle retry
      }
    }
  }

  // Execute individually (multicall not available or failed)
  for (let i = 0; i < calls.length; i++) {
    // Check for pause before each call
    if (queue.pausedUntil > Date.now()) {
      await sleep(queue.pausedUntil - Date.now());
    }

    // Wait between individual calls
    if (i > 0) {
      const elapsed = Date.now() - queue.lastRequestTime;
      if (elapsed < MIN_INTERVAL_MS) {
        await sleep(MIN_INTERVAL_MS - elapsed);
      }
    }
    queue.lastRequestTime = Date.now();

    try {
      const result = await rawRpcRequest(item.rpcUrl, 'eth_call', [
        { to: calls[i].target, data: calls[i].callData },
        'latest'
      ]) as string;
      resolvers[i]({ success: true, data: result });
      // Reset retry counter after successful call so each call gets its own retry budget
      item.retries = 0;
    } catch (err) {
      if (isRateLimitError(err)) {
        // Always pause on rate limit
        queue.pausedUntil = Date.now() + RATE_LIMIT_PAUSE_MS;

        if (item.retries < MAX_RETRIES) {
          console.log(`RPC: rate limited during batch, pausing ${RATE_LIMIT_PAUSE_MS}ms, retry ${item.retries + 1}/${MAX_RETRIES}`);

          // Create new batch item for remaining calls (current + rest)
          const remainingItem: QueueItem = {
            type: 'batch',
            rpcUrl: item.rpcUrl,
            method: 'eth_call',
            params: [],
            resolve: () => {},
            reject: () => {},
            retries: item.retries + 1,
            batchCalls: calls.slice(i),
            batchResolvers: resolvers.slice(i),
          };
          queue.items.unshift(remainingItem);
          return;
        } else {
          // Max retries exceeded - fail all remaining calls
          console.log(`RPC: max retries exceeded during batch, failing remaining ${calls.length - i} calls`);
          for (let j = i; j < calls.length; j++) {
            resolvers[j]({ success: false, data: '0x' });
          }
          return;
        }
      }
      resolvers[i]({ success: false, data: '0x' });
    }
  }
}

/**
 * Add a single request to the queue
 */
function enqueue(
  chainId: number,
  rpcUrl: string,
  method: string,
  params: unknown[]
): Promise<unknown> {
  return new Promise((resolve, reject) => {
    const queue = getQueue(chainId);
    queue.items.push({
      type: 'single',
      rpcUrl,
      method,
      params,
      resolve,
      reject,
      retries: 0,
    });
    processQueue(chainId);
  });
}

/**
 * Flush pending batch calls into the queue
 */
function flushBatch(chainId: number): void {
  const queue = getQueue(chainId);

  if (queue.batchCollectTimer) {
    clearTimeout(queue.batchCollectTimer);
    queue.batchCollectTimer = null;
  }

  if (queue.pendingBatchCalls.length === 0 || !queue.rpcUrl) return;

  const calls = queue.pendingBatchCalls.splice(0);
  const resolvers = queue.pendingBatchResolvers.splice(0);
  queue.pendingBatchRejecters.splice(0);

  queue.items.push({
    type: 'batch',
    rpcUrl: queue.rpcUrl,
    method: 'eth_call',
    params: [],
    resolve: () => {},
    reject: () => {},
    retries: 0,
    batchCalls: calls,
    batchResolvers: resolvers,
  });

  processQueue(chainId);
}

/**
 * Add a call to the batch collector
 */
function enqueueBatch(
  chainId: number,
  rpcUrl: string,
  target: `0x${string}`,
  callData: `0x${string}`
): Promise<{ success: boolean; data: string }> {
  return new Promise((resolve, reject) => {
    const queue = getQueue(chainId);

    queue.pendingBatchCalls.push({ target, callData });
    queue.pendingBatchResolvers.push(resolve);
    queue.pendingBatchRejecters.push(reject);
    queue.rpcUrl = rpcUrl;

    // Flush immediately if we hit max batch size
    if (queue.pendingBatchCalls.length >= MAX_BATCH_SIZE) {
      flushBatch(chainId);
      return;
    }

    // Set timer to flush batch after collection period
    if (!queue.batchCollectTimer) {
      queue.batchCollectTimer = setTimeout(() => {
        queue.batchCollectTimer = null;
        flushBatch(chainId);
      }, BATCH_COLLECT_MS);
    }
  });
}

/**
 * Check multicall availability (queued)
 */
async function checkMulticall(chainId: number, rpcUrl: string): Promise<boolean> {
  const queue = getQueue(chainId);

  if (queue.multicallChecked) {
    return queue.multicallAvailable ?? false;
  }

  // Store rpcUrl for later use
  queue.rpcUrl = rpcUrl;

  try {
    const testData = encodeFunctionData({
      abi: Multicall3Abi,
      functionName: 'aggregate3',
      args: [[]],
    });

    await enqueue(chainId, rpcUrl, 'eth_call', [
      { to: MULTICALL3_ADDRESS, data: testData },
      'latest'
    ]);

    queue.multicallAvailable = true;
  } catch {
    queue.multicallAvailable = false;
  }

  queue.multicallChecked = true;
  return queue.multicallAvailable ?? false;
}

// ============ Public API ============

export async function queuedEthCall(
  chainId: number,
  client: RpcClient,
  to: `0x${string}`,
  data: `0x${string}`
): Promise<string> {
  const rpcUrl = getRpcUrl(client);
  await checkMulticall(chainId, rpcUrl);
  const result = await enqueueBatch(chainId, rpcUrl, to, data);
  if (!result.success) throw new Error('Call reverted');
  return result.data;
}

export async function queuedGetStorageAt(
  chainId: number,
  client: RpcClient,
  address: `0x${string}`,
  slot: `0x${string}`
): Promise<string> {
  const rpcUrl = getRpcUrl(client);
  const result = await enqueue(chainId, rpcUrl, 'eth_getStorageAt', [address, slot, 'latest']);
  return result as string;
}

export async function batchedEthCalls(
  chainId: number,
  client: RpcClient,
  calls: Array<{ target: `0x${string}`; callData: `0x${string}` }>
): Promise<Array<{ success: boolean; data: string }>> {
  const rpcUrl = getRpcUrl(client);
  await checkMulticall(chainId, rpcUrl);
  const promises = calls.map(c => enqueueBatch(chainId, rpcUrl, c.target, c.callData));
  return Promise.all(promises);
}

export function encodeCall(
  abi: Abi,
  functionName: string,
  args?: unknown[]
): `0x${string}` {
  return encodeFunctionData({
    abi,
    functionName: functionName as never,
    args: args as never,
  });
}

export function decodeCallResult<T>(
  abi: Abi,
  functionName: string,
  data: string
): T {
  return decodeFunctionResult({
    abi,
    functionName: functionName as never,
    data: data as `0x${string}`,
  }) as T;
}

export function resetMulticallAvailability(chainId: number): void {
  const queue = queues.get(chainId);
  if (queue) {
    queue.multicallAvailable = null;
    queue.multicallChecked = false;
  }
}
