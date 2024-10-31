

export function toDecimalUnit(amount: number, decimals?: number): number {
  let factor = Math.pow(10, decimals || 18);
  return amount / factor;
}

export function toReadableAmount(amount: number | bigint, decimals?: number, unit?: string, precision?: number): string {
  if(!decimals)
    decimals = 18;
  if(!precision) 
    precision = 3;
  if(!amount)
    return "0"+ (unit ? " " + unit : "");
  if(typeof amount === "bigint")
    amount = Number(amount);

  let decimalAmount = toDecimalUnit(amount, decimals);
  let precisionFactor = Math.pow(10, precision);
  let amountStr = (Math.round(decimalAmount * precisionFactor) / precisionFactor).toString();

  return amountStr + (unit ? " " + unit : "");
}
