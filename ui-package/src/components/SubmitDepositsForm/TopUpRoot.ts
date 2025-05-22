
export async function topupRoot(pubkeyHex: string, amountGwei: bigint) {
  function hexToBytes(hex: string) {
    if (hex.startsWith("0x")) hex = hex.slice(2);
    const bytes = new Uint8Array(hex.length / 2);
    for (let i = 0; i < bytes.length; i++) {
      bytes[i] = parseInt(hex.slice(i * 2, i * 2 + 2), 16);
    }
    return bytes;
  }

  const pubkey = hexToBytes(pubkeyHex);
  if (pubkey.length !== 48) throw new Error("pubkey must be 48 bytes");

  const withdrawal_credentials = new Uint8Array(32);

  function toLittleEndian64(value) {
    const buffer = new ArrayBuffer(8);
    const view = new DataView(buffer);
    view.setBigUint64(0, BigInt(value), true);
    return new Uint8Array(buffer);
  }

  function concat(...arrays) {
    const totalLength = arrays.reduce((sum, arr) => sum + arr.length, 0);
    const result = new Uint8Array(totalLength);
    let offset = 0;
    for (let arr of arrays) {
      result.set(arr, offset);
      offset += arr.length;
    }
    return result;
  }

  async function sha256(data) {
    const hash = await crypto.subtle.digest("SHA-256", data);
    return new Uint8Array(hash);
  }

  const amountBytes = toLittleEndian64(amountGwei);
  const pubkeyRoot = await sha256(concat(pubkey, new Uint8Array(16)));

  const zero32 = new Uint8Array(32);
  const signatureRoot = await sha256(concat(
    await sha256(concat(zero32, zero32)),
    await sha256(concat(zero32, zero32))
  ));

  const depositDataRoot = await sha256(concat(
    await sha256(concat(pubkeyRoot, withdrawal_credentials)),
    await sha256(concat(amountBytes, new Uint8Array(24), signatureRoot))
  ));

  return depositDataRoot;
}
