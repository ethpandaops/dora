

export function hexToBytes(hex: string): Uint8Array {
  if (hex.startsWith("0x")) {
    hex = hex.slice(2);
  }
  if (hex.length & 1) {
    throw Error("hexToBytes:length must be even " + hex.length);
  }
  const n = hex.length / 2;
  const a = new Uint8Array(n);
  for (let i = 0; i < n; i++) {
    a[i] = parseInt(hex.slice(i * 2, i * 2 + 2), 16);
  }
  return a;
}

export function bytesToHex(bytes: Uint8Array): string {
  let s = "";
  const n = bytes.length;
  for (let i = 0; i < n; i++) {
    s += ("0" + bytes[i].toString(16)).slice(-2);
  }
  return "0x" + s;
}
