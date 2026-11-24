# Synchronization

## Clients (one-way, default)

Unchanged in principle, hardened in scheduling:

* Clients of the master that reference a node's adopted inbounds are the
  **desired state** for that node. `NodeSyncService.Reconcile` reads the node's
  `@cluster` group (`actualClusterClients`), diffs (`clientDiffers`) and pushes
  only differences through the node's `save` action. Local users of the node
  (outside `@cluster`) are never touched.
* Triggers: any client/inbound change marks nodes **dirty**
  (`bumpDirtyGen`, `nodes.dirty`); the 5 s node job reconciles dirty online
  nodes (`ReconcileDirtyOnline`); an hourly job reconciles all online nodes
  (`ReconcileAllOnline`); *Sync now* forces one.
* Idempotent by construction: a reconcile that is interrupted or repeated
  converges to the same state.
* Backoff: after a failed background reconcile the next try waits 30 s, doubled
  per consecutive failure, capped at 15 min (`syncBackoffFor`). Interactive
  reconciles ignore the backoff. Three consecutive failures mark the node
  `degraded`; the Audit / Sync tab shows last attempt, last success, failures,
  last error and next retry.
* While a reconcile runs the node shows `syncing`.
* Traffic is collected every minute and folded into the master's counters.

## Bidirectional (opt-in)

Unchanged: see [../bidirectional-sync.md](../bidirectional-sync.md).

## Remote administrative operations

Not queued. They run synchronously, are retried on transport errors (3
attempts, 300 ms / 600 ms with ±20 % jitter) reusing the same `X-Request-Id` so
the node deduplicates a retried write, and are refused while the node's circuit
breaker is open. After a successful write the node is marked dirty and
reconciled, so the master's replicas and the subscription links built from
them follow the node's new inbounds.

## Offline nodes

* Heartbeat: 3 consecutive failures open the breaker; probes then back off
  5 s → 2 min (±20 %); the card keeps the last metrics marked stale.
* Subscriptions keep serving the node's stored links.
* Client changes accumulate as the dirty flag and are applied on reconnect.
* *Reconnect* clears the breaker and probes immediately.
* The breaker state is stored in the `nodes` row, so a master restart keeps
  backing off a failing node.
* Writes can be queued instead of refused (`queue=true`, *Queue* tab switch):
  stored sealed in `node_queued_ops`, replayed by the 5 s job in order per
  node once it is online, with the original request id; backoff 30 s → 30 min,
  20 attempts, then `failed` (retry / cancel from the Queue tab). Core stop,
  panel restart/update and token rotation are never queued.

## Edits made directly on a node

The node's status carries `sys.inboundsRev` (a hash of its inbounds,
recomputed only when its change counter moves). The master compares it on
every heartbeat (5 s) and, when it changed, refreshes that node's replicas and
the subscription links in the background — no wait for the hourly reconcile.
