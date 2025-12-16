/**
 * Converts a raw amount to a decimal unit
 * @param amount The amount to convert
 * @param decimals The number of decimals (default: 18)
 * @returns The amount in decimal units
 */
export function toDecimalUnit(amount: number, decimals?: number): number {
  let factor = Math.pow(10, typeof decimals === "number" ? decimals : 18);
  return amount / factor;
}

/**
 * Converts a raw amount to a human-readable string with proper decimal places and optional unit
 * @param amount The amount to format
 * @param decimals The number of decimal places in the raw amount (default: 18 for LYX)
 * @param unit Optional unit to append to the result (e.g., "LYX", "GWEI")
 * @param precision Number of decimal places to show in the result
 * @returns Formatted string with the readable amount
 */
export function toReadableAmount(amount: number | bigint, decimals?: number, unit?: string, precision?: number): string {
  if(typeof decimals !== "number")
    decimals = 18;
  if(typeof precision !== "number")
    precision = 3;
  if(!amount)
    return "0"+ (unit ? " " + unit : "");
  if(typeof amount === "bigint")
    amount = Number(amount);

  let decimalAmount = toDecimalUnit(amount, decimals);
  let precisionFactor = Math.pow(10, precision);
  let amountStr = (Math.round(decimalAmount * precisionFactor) / precisionFactor).toFixed(precision);
  while (amountStr.endsWith("0")) {
    amountStr = amountStr.slice(0, -1);
  }
  if(amountStr.endsWith("."))
    amountStr = amountStr.slice(0, -1);

  return amountStr + (unit ? " " + unit : "");
}
