// Shared color coding & labels for withdrawal credential type prefixes.
// Colors match dora's server-side formatter (utils/format.go formatWithdrawalHash)
// so credentials look the same on every page.
export const CREDENTIAL_TYPE_INFO: { [prefix: string]: { label: string; className: string } } = {
  '00': { label: '0x00 - BLS withdrawal credentials (legacy, 32 ETH max effective balance)', className: 'text-warning' },
  '01': { label: '0x01 - Execution withdrawal credentials (32 ETH max effective balance)', className: 'text-success' },
  '02': { label: '0x02 - Compounding withdrawal credentials', className: 'text-info' },
  'b0': { label: '0xB0 - Builder credentials', className: 'text-primary' },
};

export function credentialTypeInfo(prefix: string): { label: string; className: string } {
  return CREDENTIAL_TYPE_INFO[prefix.toLowerCase()] ?? { label: `0x${prefix} - Unknown credential type`, className: 'text-warning' };
}
