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
| Secrets at rest | node token and mTLS key sealed with AES-256-GCM (`enc:v1:`), key in `<db dir>/secret.key` (0600) or `SUI_SECRET_KEY`; plaintext rows migrated on start; `3sui secrets -decrypt` for downgrades | `util/secretbox`, `database/migration_seal_secrets.go`, `model.SecretString` |
| mTLS | optional client certificate per node (master side) + `apiClientCA` setting (node side: apiv2 requires a verified client cert) | `service/nodeMTLS.go`, `service/node.go`, `web/web.go` |
| Persistence | idempotency records (hashed key, sealed body), breaker state, offline write queue (`node_queued_ops`, sealed payload, ordered per node, backoff 30 s → 30 min, 20 attempts, cancel/retry) | `api/idempotency.go`, `service/nodeHealth.go`, `service/nodeQueue.go` |
| Fast pickup of direct node edits | node publishes `sys.inboundsRev`; the 5 s heartbeat refreshes replicas and links when it moves | `service/nodeExtras.go` |
| Remote panel update | `panel.updateInfo` / `panel.update` (confirm + pinned version) / `panel.updateStatus` through the node's own checksum-verified updater | `service/nodeExtras.go`, `service/nodeRemote.go` |
| Node tree | `nodes.tree`: the node's own nodes (one level, no credentials) | `service/nodeExtras.go` |
| Traffic / Clients tabs | per-tag traffic history from the node's stats (`stats` op), clients with online state (`onlines` op) | `frontend/src/views/NodeDetail.vue` |
| Subscription headers | the full LucX header set incl. Happ-specific headers and X-HWID device limit; see [subscription-headers.md](subscription-headers.md) | `service/subProfile.go`, `service/subRouting.go`, `sub/profileHeaders.go`, `service/clientHwid.go` |
| Pre-existing failures fixed | 4 tests failing on `main` (nil DB in goroutines/tx, bisync keep-local, `NowUnix` leak, backup of `sync_conflicts`) | first commit |

## Not done / limitations

* **mTLS on the node** needs the panel itself to terminate TLS (its own
  certificate or ACME). Behind nginx/a CDN the client certificate never reaches
  the panel: with `apiClientCA` set, apiv2 then refuses every request (fail
  closed). The browser UI is unaffected (`VerifyClientCertIfGiven`).
* **Encryption key**: a backup restored on another machine needs the same
  `secret.key` (or `SUI_SECRET_KEY`); otherwise node tokens read as empty and
  must be re-entered. Downgrade: run `3sui secrets -decrypt` first.
* **Queue**: opt-in per request (`queue=true`, UI switch on the Queue tab);
  dangerous ops are never queued. Idempotency records live 10 min, so a queued
  write retried later than that after an answer was lost may apply twice
  (saves of the same object by tag are refused as duplicates by the node).
* **Node tree** is one level (no recursive walk of grand-children).
* **Rate limiter** stays in memory (per process).
* **Clients tab** is read-only by design (clients are managed on the master).
* JSON editors on the details page are plain text areas.
* Locales other than en/ru fall back to English for the new strings.

## Licensing

TARGET is GPL-3.0. SOURCE (LucX) is GPL-3.0 with PolyForm Noncommercial 1.0.0
components (`internal/lucx/**`, `internal/awg/**`,
`internal/web/service/node_contract.go`, `inbound_sublink.go`, tunnel/awg
controllers and jobs, and others carrying `SPDX-License-Identifier:
PolyForm-Noncommercial-1.0.0`). Nothing was copied from SOURCE; the
implementation is independent and written against 3S-UI's code. No new
third-party dependencies were added (the e2e helper uses only the Python
standard library).
