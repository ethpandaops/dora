# dora-replay

Step a past slot range through Dora as if it were happening live.

`dora-replay` serves a fake beacon node and a fake execution node backed by a real
upstream, and drives a virtual clock that the explorer follows. A past slot range then
unfolds slot by slot with pause, step, seek and play, while the normal Dora UI stays
live and inspectable — the same indexer, service and handler code paths that run against
a real chain.

```
                    ┌───────────────┐        ┌──────────────────────┐
   dora-explorer ──►│  dora-replay  │──────► │ beacon node (archive)│
   (replay: on)     │  CL  :15052   │        │ execution node       │
        ▲           │  EL  :15545   │──────► │ tracoor (states)     │
        └───clock───│  ctl :15000   │        └──────────────────────┘
                    └───────────────┘
                       replay> step 32
```

## Running one

```sh
go build -o bin/dora-replay ./cmd/dora-replay

./bin/dora-replay \
  --upstream       https://user:pass@bn-archive-1.example \
  --el-upstream    https://user:pass@rpc-1.example \
  --tracoor        https://tracoor.example \
  --tracoor-network my-devnet \
  --start-slot     48000 \
  --cache-dir      ./temp/replay-cache
```

Then start Dora with a config whose `beaconapi`/`executionapi` endpoints point at the
two listeners and whose `replay` block points at the control listener:

```yaml
replay:
  enabled: true
  controlUrl: "http://127.0.0.1:15000"

beaconapi:
  endpoints:
    - url: "http://127.0.0.1:15052"
      name: "replay-cl"

executionapi:
  endpoints:
    - url: "http://127.0.0.1:15545"
      name: "replay-el"
```

`replay/example-config.yaml` is a complete working example.

## Controlling it from the explorer

With `replay.controlUrl` set, the explorer side-loads a control panel from the replay
process and hangs it off the header: a collapsed pill showing the replayed epoch and
what the replay is doing, expanding into live state and controls.

```
                                   ● REPLAY  epoch 1442 · playing 6x  ▼
  ┌──────────────────────────────────────────────┐
  │ Slot         46154 → 48960                   │
  │ Epoch        1442 of 1624 upstream           │
  │ Time         2026-08-19 15:50:48Z            │
  │ Head         46154  0x28a442f2…              │
  │ Checkpoints  justified 1440 · finalized 1439 │
  │ EL block     45376                           │
  │ ▓▓▓▓▓▓▓▓▓▓▓▓░░░░░░░│░░░░░░░░░░░░░░░░░░░░░░░  │
  │ from 44800                  chain head 51968 │
  │ [ Stop ] [+1] [+1 epoch]  [ 6x ▾ ]           │
  │ [ 48960          ] [ Forward to ]            │
  └──────────────────────────────────────────────┘
```

The explorer's whole share of this is one template block that sets
`window.doraReplayApi` and loads `<controlUrl>/replay/ui.js`. The UI itself — markup,
styling, API calls — is embedded in the replay binary and talks to its control API
directly, so it can be changed without rebuilding or restarting the explorer.

The panel also retunes how often the explorer's polling pages refresh, by setting
`window.doraIndexRefreshInterval` from the replay's current pace. At 16x a slot goes by
in well under a second, and the stock 15-second refresh would leave the index page
several epochs behind what the replay is actually showing; while paused it falls back to
the stock interval.

## Control API

Served on `--control-listen`, with CORS open so the browser can reach it from the
explorer's origin.

| Endpoint | Purpose |
|---|---|
| `GET /replay/clock` | `{time_ms, rate}` — the virtual clock the explorer follows |
| `GET /replay/status` | full state snapshot (see below) |
| `GET /replay/events` | SSE stream of `status` events, pushed on every change |
| `POST /replay/command` | drive the replay; returns the new status |
| `GET /replay/ui.js` | the side-loaded control panel |

Commands mirror the console:

```jsonc
{"action": "play",    "speed": 6}     // speed 0 = as fast as upstream allows
{"action": "speed",   "speed": 4}     // change the rate without starting or stopping
{"action": "step",    "slots": 32}
{"action": "forward", "slot": 48960, "speed": 6}
{"action": "stop"}
{"action": "start"}                   // resume, keeping the speed and any target
```

The status carries the replayed position (`virtual_slot`, `virtual_epoch`,
`virtual_time`, `rate`), the chain state at that point (`head_slot`, `head_root`,
`justified_epoch`, `finalized_epoch`, `execution_block`), the drive state (`running`,
`speed`, `start_slot`, `target_slot`) and the chain context needed to render it
(`upstream_slot`/`upstream_epoch` — the head of the *real* chain, re-read every 30s —
plus `genesis_time`, `slot_duration_ms`, `slots_per_epoch`).

Status events are lossy on purpose: a snapshot that a stalled browser tab did not read
is dropped rather than queued, so the UI can never hold the replay up. Chain events on
the fake beacon node are the opposite — never dropped, see below.

## The console

```
replay> status
  slot 48160  epoch 1505  head 48160 [0x513df450…]
  justified 1503  finalized 1502  el block 47370
  playing 6x  time 2026-08-20T04:32:08Z  streams 2
  upstream https://eth:xxxxx@bn-grandine-besu-1.example (+tracoor)

replay> step             # advance one slot, then pause
replay> step 32          # advance an epoch
replay> forward 48960    # run to a slot as fast as upstream allows
replay> forward 48960 6x # run to a slot at 6x real time
replay> play 4x          # run on without a target
replay> stop             # pause; the virtual clock freezes where it is
replay> start            # resume, keeping the speed and any target
replay> quit
```

Pick the speed from what the explorer has to keep up with. Stepping runs as fast as the
upstream answers, which is far quicker than the explorer can index; a `play`/`forward`
speed gives it a fixed budget per slot instead. On a 12-second-slot devnet, 6x leaves
two real seconds per slot, which is comfortable for block indexing and epoch stats.

## Waiting for state loads

Once per epoch the explorer pulls a full beacon state — tens of megabytes to fetch,
decompress and decode, during which it cannot process anything else. Nothing is lost
when that happens (every link from the replay to the indexer applies backpressure rather
than dropping), but the virtual clock would otherwise keep running, so by the time the
state arrived the explorer would be several slots behind the slot the replay claims to
be at.

So halfway through every slot the replay checks whether a state is still on its way to
the explorer, and if so **freezes the clock** until it arrives. The wait costs real time
and no virtual time: the explorer's own clock mirrors the frozen rate, so it stays
exactly where the replay left it instead of drifting. The console and the control panel
both report this as `holding for N state load(s)`.

A read that never finishes cannot wedge a run — the gate gives up after
`--state-hold-timeout` (default 2m) and logs a warning.

## Upstreams

**Beacon node** (`--upstream`, required) answers everything: genesis, specs, headers,
blocks, blob sidecars, payload envelopes, finality. Point it at **one** node that keeps
historical data, not at a load balancer — a fan-out over mixed-retention backends fails
intermittently mid-replay. Nodes differ a lot here: many prune all but the most recent
states even when they are labelled "archive".

**Tracoor** (`--tracoor`, optional but recommended) keeps every beacon state and block it
sampled, addressed by root. **States** are read from it first: it has the depth a replay
needs, where beacon nodes prune all but the most recent, and it spares the devnet from
serving a ~17 MB state per epoch. **Blocks are not** — the node still has every block of
the replayed range and answers in one round trip, where a tracoor read costs a lookup
plus a download; tracoor only backs blocks up when the node 404s one. It stores SSZ
only, so a JSON-only client falls back to the node. A tracoor-only run is rejected —
tracoor cannot resolve "the block at slot N".

**Execution node** (`--el-upstream`, optional) backs the JSON-RPC proxy. Without it the
execution proxy is not started and the explorer runs consensus-only.

**Cache** (`--cache-dir`, optional) records every immutable artifact fetched, so a range
is downloaded once and replays offline and reproducibly afterwards.

## What the fake nodes do

The consensus proxy passes everything through except where the virtual head matters:

* `/eth/v1/node/syncing` is synthesized: head at the replayed slot, never syncing
* `head`, `finalized` and `justified` identifiers resolve to the roots the chain had at
  the virtual head, not the ones the upstream has today
* any slot beyond the virtual head answers 404, the same as a node that has not seen it
* `/eth/v1/beacon/states/head/finality_checkpoints` is answered from the finality the
  replay tracks — reading it upstream would force a historical state load
* `/eth/v1/events` is generated locally: `execution_payload_bid` → `block` → `head` →
  `execution_payload_available`, plus `finalized_checkpoint` when it moves

The execution proxy tracks a virtual head derived from block timestamps (a payload's
timestamp is its slot's time, so the execution head follows the consensus head exactly):

* `latest`/`safe`/`finalized` are pinned to the virtual head, and a numeric block beyond
  it answers as if the node did not have it yet
* `eth_getLogs` is clamped to the virtual head
* `eth_newBlockFilter`/`eth_getFilterChanges`/`eth_uninstallFilter` are served locally,
  handing out the blocks that appeared since the last poll
* everything else is forwarded, with batches forwarded as batches

## Dora's side

Everything the explorer knows about "now" runs through `ChainState`. In replay mode it
polls `GET /replay/clock` and interpolates between polls, `CurrentSlot()` follows that
virtual time, and a small ticker replaces `ethwallclock` (which reads `time.Now()`
internally and cannot be paused) to fire the slot and epoch dispatchers. Genesis stays
the real one, so `SlotToTime` keeps returning true historical timestamps — only "now"
moves. Page timestamps use the same clock, so "x ago" is computed against the simulated
present.

The only other thing Dora contributes is one block in the page layout, which sets
`window.doraReplayApi` and side-loads `<controlUrl>/replay/ui.js`. Everything the
control panel is lives in the replay binary.

That is the whole change on Dora's side. No indexer, service or handler code is aware of
the replay, which is the point: the run has to exercise the real code paths.

## Starting mid-chain

A replay starts in the middle of the chain, so use a fresh database, `blockdb`,
`statecache` and pubkey-cache path per run. Dora takes its finalized epoch from the
chain, so it will not try to finalize from epoch 0 — but the synchronizer starts from
whatever `indexer.syncstate` says. Two options:

* `indexer.disableSynchronizer: true` — index only from the start slot forward. Fast to
  get going; everything before the start slot is simply absent.
* leave the synchronizer on — it backfills epochs 0..start in the background while the
  replay steps forward, ending with a complete database. Much slower, but the result can
  be copied and reused as the starting point for later runs.

## Fidelity limits

* **Reorgs are not replayed.** The proxy serves the canonical chain as it exists today,
  so blocks that were briefly head during the original run never appear and fork handling
  is not exercised.
* **Intra-slot timing is approximated.** Events are emitted at fixed points in the slot
  (block at ⅓, payload at ⅔) rather than when they actually arrived, so anything
  timing-sensitive within a slot will not reproduce.
* **Real timers keep running.** Dora's retry and backoff timers run on real time, so a
  long pause still logs client-health retries. Client readiness is not wallclock-gated,
  so pausing is otherwise safe.
* **Execution head is inferred** from block timestamps rather than observed, so a slot
  whose payload was never revealed shows up as "no new execution block" — which matches
  reality, but by derivation.
* **Historical execution state** (`eth_getBalance` at an old block) needs an archive
  execution node; a full node only keeps a shallow window.
