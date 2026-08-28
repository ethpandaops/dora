/**
 * Gas limits for the transactions submitted from the explorer's submit pages.
 *
 * Each value is the measured worst case of the target contract plus headroom.
 * The worst case assumes every storage slot the call touches is cold and gets
 * created (written from zero), which is what the request predeploys do whenever
 * the queue grows past its previous high-water mark and after the per-block
 * system call has reset the request count and the queue pointers.
 *
 * Slot creation dominates the cost from the Amsterdam fork on: EIP-8037 prices
 * a created slot at 64 state bytes * 1530 gas = 97,920 state gas on top of
 * 5,000 regular gas, replacing the flat 20,000 + 2,100 charged before, so every
 * created slot adds 80,820 gas. State gas is drawn from the transaction gas
 * limit as well (the separate reservoir only holds what exceeds EIP-7825's
 * 16,777,216 regular-gas cap), so the limit has to cover both dimensions.
 *
 * Worst cases measured with `evm t8n --state.fork Amsterdam` against the
 * deployed predeploy bytecode, with an all non-zero calldata payload:
 *
 *   deposit contract, deposit()   246,366  (branch node + deposit_count created)
 *   EIP-7002 withdrawal request   541,333  (count, tail, 3 queue slots created)
 *   EIP-7251 consolidation        645,234  (count, tail, 4 queue slots created)
 *   EIP-8282 builder exit         541,141  (count, tail, 3 queue slots created)
 *   EIP-8282 builder deposit      852,938  (count, tail, 6 queue slots created)
 *
 * The request contracts also run the EIP-1559 style fee loop, which grows with
 * the excess request counter (~14 gas per unit of excess). The numbers above
 * include an excess of 100; the headroom covers far beyond what the fee can
 * realistically reach, since the fee itself grows as e^(excess/17) wei.
 */

/** Deposit contract `deposit()`, used for both new deposits and top-ups. */
export const DEPOSIT_GAS_LIMIT = 300000n;

/** EIP-7002 withdrawal request predeploy (partial withdrawals and exits). */
export const WITHDRAWAL_REQUEST_GAS_LIMIT = 600000n;

/** EIP-7251 consolidation request predeploy. */
export const CONSOLIDATION_REQUEST_GAS_LIMIT = 700000n;

/** EIP-8282 builder exit request predeploy. */
export const BUILDER_EXIT_REQUEST_GAS_LIMIT = 600000n;

/** EIP-8282 builder deposit request predeploy. */
export const BUILDER_DEPOSIT_REQUEST_GAS_LIMIT = 950000n;
