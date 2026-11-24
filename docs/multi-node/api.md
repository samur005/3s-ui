# Multi-node API

Two surfaces with the same handlers:

* **Session API** (browser): `POST|GET <panel>/api/<action>` with the login cookie.
* **Token API**: `POST|GET <panel>/apiv2/<action>` with header `Token: <api token>`.

Every answer is `{"success": bool, "msg": string, "obj": any}`.

## Master side (manage nodes)

| Method | Action | Parameters | Returns |
|---|---|---|---|
| POST | `nodeRemote` | `id`, `op`, `action` (save ops: `new`/`edit`/`del`), `data` (JSON), `params` (JSON object of strings), `confirm` (`true` for dangerous ops) | the node's `obj` |
| GET | `nodeDetail` | `id` | `{node, status, capabilities, legacy, capsKnown, capsError?, adopted[], operations[], remoteOps[], masterApiVersion}` |
| GET | `nodeOps` | `id` (optional), `limit` (≤ 200), `offset` | `{rows: NodeOperation[], total}` |
| POST | `nodeBulk` | `ids` (JSON array), `op` (`enable`, `disable`, `reconnect`, `reconcile`, `subExclude`, `subInclude`, `refreshCaps`, `setGroup`), `value` (group) | `[{nodeId, ok, error?}]` |
| POST | `nodeReconnect` | `id` | fresh `NodeStatus` |
| POST | `nodeTokenRotate` | `id` | — |
| GET | `nodeQueue` | `id` (optional), `limit` | queued operations (no payloads) |
| POST | `nodeQueueCancel` / `nodeQueueRetry` | `id` (queue row) | — |
| POST | `nodeInboundSave` | `action` (`new`/`edit`/`del`), `nodeId` (`new`), `data` (inbound JSON; the tag as a JSON string for `del`), `initUsers` (`new`: comma-separated client ids), `requestId` (optional, ≤ 64; a retried submit with the same id is a replay) | `{id, node_id, tag, type}` of the replica (`new`/`edit`) |
| GET | `nodeInboundForm` | `nodeId`, or `id` (a replica) | `{nodeId, tls[], inbound?}`: the node's TLS configs and, for `id`, the node's own inbound under the replica id |
| GET | `clientHwids` | `id` (client) | registered devices (hash shortened) |
| POST | `clientHwidsDel` | `id` (client), `device` (row id, empty/0 = all) | — |

`nodeInboundSave` is the master inbound form's node target: `new` checks the
tag on the master and the port on the node (`portCheck`), writes the inbound
through `save.inbounds`, adopts it as a replica, binds `initUsers` and
reconciles at once; `edit` writes a replica's edit to its node inbound (tag
changes refused) and refreshes the replica; `del` deletes the inbound on the
node, then the replica. A node that is disabled, offline or unsupported is
refused with the reason; nothing is queued.

`nodeRemote` also takes `queue=true`: if the node is unreachable the write is
queued and the answer is `{queued: true, queueId}`.

New ops: `onlines`, `stats` (`resource` = inbound/outbound/user/endpoint,
`tag`, `period` = hour/day/week/month/60day/90day), `panel.updateInfo`,
`panel.updateStatus`, `panel.update` (confirm; `version` = the release you
approved), `nodes.tree`.

Existing endpoints kept: `save` with `object=nodes` (CRUD), `testNode`,
`reconcileNode`, `nodeInbounds`, `adoptInbounds`, `load` (`nodes`, `nodesStatus`).

`params` per op: `logs` → `count` (≤ 500), `level`; `inbounds`/`clients` → `id`;
`portCheck` → `port`, `network` (`tcp`/`udp`), `exclude` (tag);
`checkOutbound` → `tag`. Save ops: `data` is the object JSON (`new`/`edit`) or
the tag as a JSON string (`del`). `config.route` / `config.dns`: `data` is the
section object.

Refusals by the master's guard rails come back as `success:false` with a
`refused: …` message and are audited with status `refused`.

## Node side (API level 2)

| Method | Action | Parameters | Returns |
|---|---|---|---|
| GET | `nodeHello` | — | `{apiVersion, minMasterApi, appVersion, coreVersion, time}` |
| GET | `nodeCapabilities` | — | `NodeCapabilities` (registries, build tags, transports, TLS, components, management ops, features) |
| GET | `nodeHealth` | — | cpu, mem, swap, disk, net counters, uptimes, core state, counts |
| GET | `nodeServices` | — | sing-box, wg, awg, qwdtt, xray sidecar status |
| GET | `nodePortCheck` | `port`, `network`, `exclude` | `{port, network, usedBy, bindable, available, detail}` |
| POST | `nodeCheckConfig` | `data` (full sing-box config JSON) | validates without saving |
| POST | `nodeRotateToken` | — | `{token, id, previousId}` — a new token for the caller's admin; the old one stays valid |
| POST | `nodeRevokeToken` | `id` | revokes a token of the same admin, never the calling one |
| GET | `nodeUpdateInfo` | — | `{current, latest, updateAvailable, canSelfUpdate, reason, docker}` |
| GET | `nodeUpdateStatus` | — | updater state |
| POST | `nodeUpdate` | `version` (optional, must equal the latest release) | starts the checksum-verified self-update |
| GET | `nodeTree` | — | this panel's own nodes: id, name, host, group, location, state, version |

`status` with `r=sys` now includes `inboundsRev`.

## Headers on apiv2 POSTs (all nodes of this release)

* `X-Request-Id: [A-Za-z0-9._-]{8,64}` — the answer is kept 10 min per
  (token user, action, id); a repeat returns the stored answer with
  `X-Idempotent-Replay: 1` instead of running the write again.
* `X-Request-Timestamp: <unix seconds>` — refused when more than 15 min off.

Both are optional; clients that do not send them behave as before.
