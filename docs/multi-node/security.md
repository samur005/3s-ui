# Security

## Threat model

| Threat | Mitigation |
|---|---|
| Stolen node token used elsewhere | token is per node, revocable on the node; rotation without a gap (`nodeTokenRotate`); tokens can carry an expiry |
| Token leaked through the master's API/UI | write-only: `GetAll` returns `tokenSet` only; the audit stores no payloads or tokens; `changes` rows redact it (`redactNodeToken`); rotation never sends the new token to the browser |
| Token sent to a host the operator did not configure | redirects only to the same host, never https→http, max 3 (`nodeCheckRedirect`); proxies from the environment are not used |
| Master used as an SSRF pivot | the dialer resolves the host itself and checks every address (`checkNodeIP`): link-local/metadata, multicast, unspecified always refused; loopback, private, CGNAT only with *Allow private address*; dials the checked IP (no DNS-rebinding window). URL limited to `scheme://host[:port]` |
| Timing attacks on tokens | node compares tokens with `subtle.ConstantTimeCompare` |
| Replay of a captured request | `X-Request-Timestamp` window ±15 min; `X-Request-Id` dedupe 10 min |
| Duplicate apply after a lost answer | same `X-Request-Id` on retry ⇒ node replays the stored answer |
| Arbitrary command execution | no shell, no generic proxy: a fixed allowlist mapped to existing node actions, typed parameters, JSON-validated and size-limited payloads (1 MiB), node responses capped (8 MiB) |
| Unsupported / dangerous config pushed | inbound/outbound/endpoint/service types checked against the node's capabilities; route/DNS sections validated by `nodeCheckConfig` before saving; `core.stop` and `panel.restart` need explicit confirmation |
| Breaking adopted replicas | deleting or renaming an adopted inbound remotely is refused |
| Flooding a node | per-node token bucket (burst 20, 2/s); breaker stops probes to a dead node |
| Silent changes | every remote write and refusal is audited (`node_operations`: who, what, when, result, attempts, duration, request id); bulk changes also write `changes` rows |
| Plain HTTP | allowed for compatibility, warned in the UI; use HTTPS or a pinned certificate |

## Limitations

* Node tokens are stored in plaintext in the master's database (as before).
  Protect the database file and backups.
* No mTLS between master and node (LucX has it; not ported). Use HTTPS with a
  verified or pinned certificate and a firewall rule for the master's IP.
* Idempotency state is in memory: a node restart forgets it (a retry after a
  restart may re-apply; client sync is idempotent anyway).
* The rate limit and breaker are per master process.

## Logging

Logs never contain tokens or payloads: remote operations are logged as
`op on node #id by actor: status [request id]`.
