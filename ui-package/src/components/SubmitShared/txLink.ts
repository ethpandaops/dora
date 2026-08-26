// Shared tx-hash display helpers for the submit pages.

export function shortTxHash(hash: string): string {
  return `${hash.substring(0, 10)}…${hash.substring(hash.length - 8)}`;
}

// Link to the configured external explorer, falling back to dora's own tx page.
export function txExplorerLink(explorerUrl: string | undefined, hash: string): string {
  return explorerUrl ? `${explorerUrl.replace(/\/$/, '')}/tx/${hash}` : `/tx/${hash}`;
}
