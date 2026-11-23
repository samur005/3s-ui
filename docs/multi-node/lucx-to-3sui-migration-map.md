# LucX → 3S-UI migration map

SOURCE: `samur005/lucx-ui-samur005` @ `98a0376c9bdff445a9595e0d4b183ad98e3d1616`
(read-only reference). TARGET: `samur005/3s-ui` @ `23649508bbc76fb8189cb8e22dedf0f36ccb42a4`.

> Note on ordering: the mapping below was decided during the audit, before
> implementation, but this file was written down after the code. LucX is
> Xray/3x-ui-based (GPL-3.0 core + PolyForm Noncommercial components);
> 3S-UI is sing-box-based (GPL-3.0). **No code was copied**: every TARGET piece
> is an independent implementation against 3S-UI's existing structures. LucX
> files under PolyForm Noncommercial (e.g. `internal/web/service/node_contract.go`,
> `internal/lucx/**`, `internal/awg/**`, tunnel/awg controllers) were read only to
> understand behaviour and were not used as a template.

| LucX feature | LucX file / symbol | 3S-UI equivalent | Status | Decision | Risk | Test |
|---|---|---|---|---|---|---|
| Node registry, write-only credentials | `internal/web/service/node.go` `NodeService.Create/Update/GetAll`, `node_contract.go` `NodeView.HasApiToken` (PolyForm) | `service/node.go` `Save`, `GetAll` (`tokenSet`) — existed | existed, extended | added `group`, `location`, `allowPrivate`, `subExclude`, `capabilities`, `capsAt` | low | `database/migration_node_allow_private_test.go`, e2e `token never returned` |
| Heartbeat | `internal/web/job/node_heartbeat_job.go` `NodeHeartbeatJob.Run/probeOne`, `service/node.go` `Probe`, `HeartbeatPatch` | `service/node.go` `RefreshAll`, `probe` — existed | existed, extended | breaker, swap/disk/net, uptimes, derived health | low | `TestDecorateHealthPrecedence`, `TestNodeOnlyClientAppearsOnlineOnMaster` |
| Capability detection | `service/node.go` `fillNodeCapability`, `runtime/remote.go` `recordCaps` (header based) | `service/nodeCaps.go` `NodeCapabilitiesInfo`, `service/nodeHealth.go` `FetchCapabilities` | new | explicit `nodeCapabilities` action from registries/build tags instead of headers | low | `TestNodeCapabilitiesInfoReportsRegistries`, e2e |
| Version compatibility | node API flags in `runtime/remote.go` (`nodeSupportsZstd`) | `NodeAPIVersion`, `NodeMinMasterAPI`, legacy detection | new | levels 1/2, legacy subset | medium (future levels) | `TestRemoteManagementAcrossNodes` (legacy), e2e compat |
| Remote transport | `internal/web/runtime/remote.go` `Remote.do`, `envelope`, `readCappedBody` | `service/node.go` `nodeGet`, `service/nodeRemote.go` `nodeCall`, `callWithRetry` | existed, extended | ctx, request id, timestamp, size cap, retry with jitter | low | `TestRemoteOfflineNodeFailsAndIsAudited` |
| Runtime abstraction local/remote | `internal/web/runtime/{runtime,local,manager}.go` `Manager`, `Remote.AddInbound/UpdateInbound/DelInbound` | not ported | not needed | 3S-UI nodes are full panels; master calls their `save` action instead of mirroring a runtime | — | — |
| Remote inbound CRUD | `runtime/remote.go` `AddInbound`, `UpdateInbound`, `DelInbound`, `wireInbound` | `NodeRemoteService` ops `save.inbounds` (+ `inbounds` read) | new | typed against node capabilities, adopted-inbound protection | medium | unit + e2e create/delete |
| Remote outbounds / endpoints / services | — (LucX manages Xray outbounds via templates) | ops `save.outbounds`, `save.endpoints`, `save.services` | new | same path as inbounds | medium | unit (type gating), UI |
| Remote routing / DNS | — | ops `config.route`, `config.dns` (`saveConfigSection`) | new | replace one section, `nodeCheckConfig` first, then node `save config` | medium (core restart) | `TestRemoteManagementAcrossNodes`, e2e refused DNS |
| Core / panel control | `runtime/remote.go` `RestartXray`, `UpdatePanel` | ops `core.restart`, `core.stop`, `core.start`, `panel.restart` | new | dangerous ops need confirm | medium | unit + e2e |
| Panel self-update on nodes | `service/node.go` `UpdatePanels` | not implemented | omitted | out of scope (touches node binaries) | — | — |
| Client push / sync | `runtime/remote.go` `AddClient`, `UpdateUser`, `DeleteClient`; `job/node_traffic_sync_job.go` `syncOne` | `service/nodeSync.go` `Reconcile`, `runReconcile`, `expectedClients`, `actualClusterClients` — existed | existed, extended | exponential backoff, sync state, `syncing` health | low | existing `nodeSync_test.go` |
| Local users protection | LucX origin GUID (`effectiveNodeGuid`) | `@cluster` group scoping — existed | existed | unchanged | low | existing tests |
| Traffic sync | `job/node_traffic_sync_job.go`, `runtime/remote.go` `FetchTrafficSnapshot` | `service/nodeSync.go` `CollectTraffic`, `cronjob/nodeTrafficJob.go` — existed | existed | unchanged | low | existing |
| Inbound adoption | `service/node.go` `MarkNodeInboundsAdopted`, `runtime.RecordAdoptedInbound` | `service/nodeSync.go` `AdoptInbounds`, replicas with `node_id` — existed | existed | remote ops refuse deleting/renaming adopted inbounds | low | `TestRemoteManagementAcrossNodes` |
| Node tree (nested masters) | `service/node_tree.go` `GetNodeTree`, `RefreshDescendants` | not ported | omitted | flat model kept | — | — |
| mTLS to nodes | `service/node_mtls.go`, `runtime/tls_client.go` | not ported; existing TLS verify / skip / SHA-256 pin kept | omitted | listed as limitation | medium | existing `node_test.go` |
| Token encryption at rest | `service/node.go` `decryptToken`, `MigrateNodeTokensToActiveKey` | not ported (token plaintext in DB, write-only in API) | omitted | limitation | medium | — |
| Token rotation | — | `NodeRemoteService.RotateToken`, `UserService.RotateCallerToken/RevokeTokenAsCaller` | new | gapless with rollback | low | `TestRemoteTokenRotation*`, e2e |
| SSRF | `internal/util/netsafe` (`AllowPrivateAddress`) | `service/nodeNet.go` `checkNodeIP`, `guardedDialContext`, `nodeCheckRedirect` | new | resolved-IP check in dialer; metadata always refused | low | `TestCheckNodeIPPolicy`, `TestNodeClientRefusesPrivateAddressUnlessAllowed`, e2e |
| Bulk operations | `service/node.go` `UpdatePanels(ids)`, `node_bulk_dispatch_test.go` | `NodeRemoteService.Bulk` | new | enable/disable/reconnect/reconcile/subExclude/subInclude/refreshCaps/setGroup | low | `TestRemoteBulkAndSubscriptionExclusion` |
| Subscription aggregation | `service/inbound_sublink.go` (PolyForm), `sub_balancer.go` | `refreshNodeLinks` (existed) + `service/nodeSubscription.go` `FilterNodeLinks` | existed, extended | policy offline/excluded/disabled/deleted, de-dup | low | `TestFilterNodeLinks`, bulk test |
| Audit | LucX event bus `nodeTransitionEvent` | `model.NodeOperation`, `ListOperations` | new | write ops + refusals, bounded to 10 000 rows | low | unit + e2e |
| Node UI | `frontend` nodes page, node modal | `Nodes.vue`, `NodeDrawer.vue`, new `NodeDetail.vue` | extended / new | tabs per spec | low | `npm run build`, headless screenshots |
| AWG / tunnels / CSQTT per node | `internal/awg/**`, `internal/lucx/tunnel/**` (PolyForm) | not ported | omitted | license + different stack; qWDTT already native in 3S-UI | — | — |
