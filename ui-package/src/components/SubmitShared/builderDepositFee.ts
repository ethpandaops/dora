// Fee math for the EIP-8282 builder deposit predeploy, shared by the builder
// deposit table and the builder topup form.
//
// Unlike EIP-7002/7251, the contract charges fees per write path: the fee
// numerator is the excess (slot 0) plus the requests already added in the
// current block (slot 1) beyond TARGET_PER_BLOCK, so the fee rises within a block.

export const BUILDER_TARGET_PER_BLOCK = 8n;
const PRE_FORK_QUEUE = 0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffn;
const LOG_LOOKBACK_RANGE = 10;

export interface IBuilderFeeInfo {
  isPreFork: boolean;
  queueLength: bigint;
  requiredFee: bigint; // exact current fee
  requestFee: bigint;  // fee incl. extra cushion (when enabled)
  batchFeeBase: bigint; // fee numerator incl. cushion, base for per-item batch fees
}

export function getRequiredFee(numerator: bigint): bigint {
  let i = 1n;
  let output = 0n;
  let numeratorAccum = 1n * 17n;
  while (numeratorAccum > 0n) {
    output += numeratorAccum;
    numeratorAccum = (numeratorAccum * numerator) / (17n * i);
    i += 1n;
  }
  return output / 17n;
}

export function computeBuilderFees(queueData: any, cachedLogData: any, addExtraFee: boolean): IBuilderFeeInfo {
  const info: IBuilderFeeInfo = {
    isPreFork: false,
    queueLength: 0n,
    requiredFee: 0n,
    requestFee: 0n,
    batchFeeBase: 0n,
  };

  if (!queueData || queueData.error || queueData.isLoading) {
    return info;
  }

  info.queueLength = queueData.queueLength;
  if (info.queueLength === PRE_FORK_QUEUE) {
    info.isPreFork = true;
    return info;
  }

  let feeNumerator = info.queueLength;
  if (queueData.blockCount > BUILDER_TARGET_PER_BLOCK) {
    feeNumerator += queueData.blockCount - BUILDER_TARGET_PER_BLOCK;
  }
  info.requiredFee = getRequiredFee(feeNumerator);

  if (addExtraFee && cachedLogData) {
    let avgRequestPerBlock = 0;
    for (let block in cachedLogData.logCount) avgRequestPerBlock += cachedLogData.logCount[block];
    avgRequestPerBlock /= LOG_LOOKBACK_RANGE;
    let extra = avgRequestPerBlock < 2 ? 3 : avgRequestPerBlock + 1;
    info.batchFeeBase = feeNumerator + BigInt(Math.ceil(extra));
    info.requestFee = getRequiredFee(info.batchFeeBase);
  } else {
    info.batchFeeBase = feeNumerator;
    info.requestFee = info.requiredFee;
  }

  return info;
}
