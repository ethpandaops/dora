import { useEffect, useState } from 'react';

// useEnsLookup reverse-resolves an execution address to its primary ENS name via
// dora's /ens/lookup endpoint. Backend resolution is asynchronous (first-seen
// addresses get queued), so a miss is retried a couple of times before giving up.
export function useEnsLookup(address: string | null): string | null {
  const [ensName, setEnsName] = useState<string | null>(null);

  useEffect(() => {
    setEnsName(null);
    if (!address) return;

    let cancelled = false;
    let attempts = 0;
    const lookup = () => {
      fetch(`/ens/lookup?address=${address}`)
        .then((res) => res.json())
        .then((data) => {
          if (cancelled) return;
          if (data.names && data.names.length > 0) {
            setEnsName(data.names[0].name);
          } else if (++attempts < 3) {
            setTimeout(lookup, 5000);
          }
        })
        .catch(() => {});
    };
    lookup();
    return () => { cancelled = true; };
  }, [address]);

  return ensName;
}
