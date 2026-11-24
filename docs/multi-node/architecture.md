# Multi-node architecture

3S-UI manages other 3S-UI panels ("nodes") from one panel ("master"). A node is
an unmodified 3S-UI install; the master talks to it over the node's own
`apiv2` (header `Token`, envelope `{success, msg, obj}`). There is no agent,
no shell, and no generic proxy: every remote operation is one allowlisted,
typed call to an existing node action.

```
            browser ──session── MASTER (3S-UI) ──apiv2 + Token──► NODE (3S-UI)
                                 │  heartbeat 5s (status, onlines)    │ its own services:
                                 │  capabilities (nodeCapabilities)   │ InboundService,
                                 │  reconcile (@cluster clients)      │ ConfigService.Save,
                                 │  remote ops (allowlist)            │ validators, core
                                 │  traffic collection                │
                                 └─ subscription (stored links) ◄─────┘
```

## Components (master)

| Piece | File | Role |
|---|---|---|
| Node registry | `database/model/model.go` (`Node`), `service/node.go` (`Save`, `GetAll`) | CRUD, URL validation, token write-only (`tokenSet`) |
| Heartbeat | `service/node.go` (`RefreshAll`, `probe`), `cronjob/nodesJob.go` | 5 s probe of `status` + `onlines`, in-memory `nodeStatuses` |
| Circuit breaker + health | `service/nodeHealth.go` | breaker after 3 failures (5 s → 2 min, ±20 % jitter); derived `health` |
| Capabilities cache | `service/nodeHealth.go` (`refreshCapabilities`, `FetchCapabilities`), DB `nodes.capabilities` | what each node can run, refreshed when the node version changes or after 30 min |
| Network policy | `service/nodeNet.go` | SSRF guard in the dialer, redirect policy, URL validation, request ids |
| Remote management | `service/nodeRemote.go` (`NodeRemoteService`) | allowlist, guard rails, retries, audit, bulk, token rotation |
| Audit | `model.NodeOperation` (`node_operations`) | one row per remote write / refusal, no payloads |
| Client sync | `service/nodeSync.go` (`Reconcile`, `runReconcile`, `claimReconcile`), `service/nodeBisync.go` | state reconciliation of the `@cluster` group (dirty flag, backoff) |
| Subscription | `service/nodeSubscription.go`, `sub/subService.go`, `sub/jsonService.go` | stored node links + exclusion / de-duplication at serve time |
| API | `api/nodeApi.go`, `api/apiHandler.go`, `api/apiV2Handler.go` | session (`api/…`) and token (`apiv2/…`) endpoints |
| UI | `frontend/src/views/Nodes.vue`, `views/NodeDetail.vue`, `layouts/drawers/node/NodeDrawer.vue` | list, bulk, details page with tabs |

## Components (node side, same binary)

| Piece | File | Role |
|---|---|---|
| Identity / capabilities | `service/nodeCaps.go` (`NodeHelloInfo`, `NodeCapabilitiesInfo`) | API level, registries read from the binary (`core.*Registry().OptionTypes()`), build tags from `debug.ReadBuildInfo`, components |
| Health / services | `service/nodeCaps.go` (`NodeHealthInfo`, `NodeServicesInfo`) | cpu/mem/swap/disk/net, uptimes, sidecars |
| Checks | `service/nodeCaps.go` (`CheckPort`, `ConfigService.CheckBaseConfig`) | port probe, sing-box config validation without saving |
| Token rotation | `service/user.go` (`RotateCallerToken`, `RevokeTokenAsCaller`) | mint a sibling token, revoke the old one |
| Idempotency / replay | `api/idempotency.go` | `X-Request-Id` dedupe (10 min), `X-Request-Timestamp` window (±15 min) |

## API levels

* **1 (legacy)** — every 3S-UI before this change. Answers `status`, `onlines`,
  `inbounds`, `outbounds`, `endpoints`, `services`, `clients`, `config`,
  `tls`, `logs`, `save`, `restartSb`, `maintenance`, `restartApp`,
  `checkOutbound`. Does not answer `nodeHello`.
* **2** — adds `nodeHello`, `nodeCapabilities`, `nodeHealth`, `nodeServices`,
  `nodePortCheck`, `nodeCheckConfig`, `nodeRotateToken`, `nodeRevokeToken`
  and idempotency headers.

The master detects the level from `nodeCapabilities` ("unknown action" ⇒ level
1) and gates operations accordingly: a level-1 node keeps monitoring, sync,
subscriptions and the legacy subset of remote operations; level-2-only
operations are refused before a request with an "upgrade the node" message.
`minMasterApi` lets a future node refuse an older master (shown as
`incompatible`).

## Health

`health` is derived on the master, precedence top to bottom:

| health | when |
|---|---|
| `disabled` | node switched off on the master |
| `offline` | probe failed (breaker may be open; `nextProbeAt` says when it retries) |
| `incompatible` | node requires a newer master API |
| `syncing` | a reconcile of this node is running |
| `degraded` | reachable, but: core stopped, latency > 1500 ms, memory or disk ≥ 95 %, ≥ 3 consecutive sync failures, capabilities unreadable, or stale metrics |
| `online` | none of the above |

`reasons[]` carries the human-readable causes.

## Design decisions

* **Extend, don't fork.** Heartbeat, sync, adoption and subscriptions existed;
  this work adds to them instead of a parallel subsystem.
* **Typed operations over the node's own API.** The node validates and applies
  with the same code its UI uses; remote and local edits cannot diverge.
* **Client sync stays state reconciliation**, not a message queue: desired
  state is recomputed from the master DB, compared with the node and patched.
  It is idempotent by construction; retries are the dirty flag with exponential
  backoff (30 s → 15 min) plus the hourly safety net. Remote admin operations
  are synchronous, retried with jitter under the same `X-Request-Id`, audited,
  and refused (not queued) while a node is offline.
* **qWDTT** is reported in capabilities (`panelInbounds`, `components.qwdtt`,
  `qwdttBinary`) and handled by the generic inbound operations; nothing in the
  remote layer is protocol-specific, so new panel types need no change here.
