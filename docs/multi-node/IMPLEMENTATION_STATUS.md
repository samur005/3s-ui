# Implementation status — Master → Remote multi-node

Branch `feat/multinode-pro`, based on `main` @ `23649508bbc76fb8189cb8e22dedf0f36ccb42a4`.

## Done

| Area | What | Where |
|---|---|---|
| Node management | CRUD (existing) + group, location, allow-private, subscription exclusion; URL validation; enable/disable; reconnect; bulk ops (enable, disable, reconnect, reconcile, subExclude, subInclude, refreshCaps, setGroup) | `service/node.go`, `service/nodeRemote.go` |
| Statuses | online / offline / degraded / incompatible / syncing / disabled (+ core-stopped, pending in UI) with reasons | `service/nodeHealth.go` |
| Version compatibility | API levels 1/2, legacy detection, `minMasterApi` | `service/nodeCaps.go`, `service/nodeRemote.go` |
| Capability discovery | registries, panel types (incl. qwdtt, wg, awg, xvless), transports, TLS/uTLS/REALITY/ECH/ACME by build tags, components, management ops; cached in DB | `service/nodeCaps.go`, `service/nodeHealth.go` |
| Monitoring | cpu, mem, swap, disk, net rates/totals, core and host uptime, latency, breaker state | `service/node.go`, `service/nodeCaps.go` |
| Typed remote operations | 26-op allowlist, no shell; inbounds/outbounds/endpoints/services CRUD; route and DNS sections with pre-check; core/panel control; logs; port and config checks | `service/nodeRemote.go` |
| User sync | existing state reconciliation, now with exponential backoff, sync state, `syncing`/`degraded`; local users untouched (`@cluster`) | `service/nodeSync.go`, `service/nodeHealth.go` |
| Subscriptions | stored node links; exclusion; de-duplication; offline/disabled/delete policy; links regenerated immediately after a node edit on the master (offline too) and after remote inbound edits — no wait for the reconcile | `service/nodeSubscription.go`, `sub/*` |
| Security | SSRF guard, redirect policy, token write-only, rotation with rollback, constant-time compare (existing), request ids, replay window, idempotency, rate limit, payload/response caps, audit, no secret logging | `service/nodeNet.go`, `api/idempotency.go`, `service/user.go` |
| Reliability | contexts and timeouts, retries with jitter, circuit breaker, bounded parallelism, pagination of the audit | `service/nodeRemote.go`, `service/nodeHealth.go` |
| DB | additive columns, `node_operations`, one-shot `migratedNodeAllowPrivate`, backup of `sync_conflicts`/`node_operations` | `database/*` |
| Frontend | Nodes page (health, groups, filters, bulk), drawer fields, token rotation, details page with 12 tabs, loading/empty/error states, en+ru | `frontend/src/**` |
| Tests | unit, integration with fake nodes, e2e with real binaries (incl. legacy node and old master), migration upgrade + rollback | `service/nodeMultinode_test.go`, `api/idempotency_test.go`, `database/migration_node_allow_private_test.go`, `scripts/multinode-e2e/` |
| Pre-existing failures fixed | 4 tests failing on `main` (nil DB in goroutines/tx, bisync keep-local, `NowUnix` leak, backup of `sync_conflicts`) | first commit |

## Not done / limitations

* **mTLS** master↔node and **token encryption at rest** (LucX has both) — not
  ported; HTTPS + pin + firewall recommended. Tokens remain plaintext in the
  master DB (write-only over the API).
* **Persistent operation queue**: remote admin operations are synchronous and
  refused while a node is offline; only client sync is deferred (dirty flag).
* **Nested masters (node tree)** and **remote panel self-update** — not
  implemented.
* **Traffic tab** shows host counters only; per-client traffic stays in the
  master's Clients page (summed by the sync); no per-node history charts.
* **Clients tab** is read-only by design (clients are managed on the master).
* JSON editors on the details page are plain text areas, not the full
  protocol forms of the local Inbounds page.
* Locales other than en/ru fall back to English for the new strings.
* Idempotency cache, rate limiter and breaker are in memory (per process).

## Licensing

TARGET is GPL-3.0. SOURCE (LucX) is GPL-3.0 with PolyForm Noncommercial 1.0.0
components (`internal/lucx/**`, `internal/awg/**`,
`internal/web/service/node_contract.go`, `inbound_sublink.go`, tunnel/awg
controllers and jobs, and others carrying `SPDX-License-Identifier:
PolyForm-Noncommercial-1.0.0`). Nothing was copied from SOURCE; the
implementation is independent and written against 3S-UI's code. No new
third-party dependencies were added (the e2e helper uses only the Python
standard library).
