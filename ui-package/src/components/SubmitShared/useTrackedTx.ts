import { useState } from 'react';
import { useWaitForTransactionReceipt } from 'wagmi';

export type TrackedTxStatus = 'idle' | 'submitting' | 'pending' | 'confirmed' | 'reverted' | 'error';

export interface TrackedTx {
  status: TrackedTxStatus;
  hash: string | null;
  errorMessage: string | null;
  start(send: () => Promise<string>): Promise<void>;
}

// useTrackedTx tracks one transaction through its full lifecycle: broadcasting
// ('submitting'), waiting for inclusion ('pending'), and its on-chain outcome
// ('confirmed' / 'reverted'). Works for both wallet-signed and locally-signed
// sends - anything that returns a tx hash.
export function useTrackedTx(onError?: (message: string) => void): TrackedTx {
  const [state, setState] = useState<{ submitting?: boolean; hash?: string | null; error?: string | null }>({});

  const receipt = useWaitForTransactionReceipt({
    hash: (state.hash ?? undefined) as `0x${string}` | undefined,
    query: { enabled: !!state.hash },
  });

  let status: TrackedTxStatus = 'idle';
  if (state.submitting) {
    status = 'submitting';
  } else if (state.error) {
    status = 'error';
  } else if (state.hash) {
    if (receipt.data?.status === 'success') status = 'confirmed';
    else if (receipt.data?.status === 'reverted') status = 'reverted';
    else status = 'pending';
  }

  const start = async (send: () => Promise<string>) => {
    if (status === 'submitting' || status === 'pending' || status === 'confirmed') return;

    setState({ submitting: true });
    try {
      const hash = await send();
      setState({ hash });
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      setState({ error: message });
      onError?.(message);
    }
  };

  return { status, hash: state.hash ?? null, errorMessage: state.error ?? null, start };
}
