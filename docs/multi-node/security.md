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
| Master database or backup stolen | node tokens and mTLS keys are sealed (AES-256-GCM, `enc:v1:`); the key is `secret.key` next to the database (0600) or `SUI_SECRET_KEY` — keep it out of the backups you hand around |
| Stolen node token used from another host | optional mTLS: the node (setting *Master client CA*) only accepts apiv2 requests with a client certificate signed by that CA; the master sends the per-node client cert |
| Queued payload exposure | queued writes and stored idempotent answers are sealed too |

## Encryption at rest

* Sealed columns: `nodes.token`, `nodes.client_key`, `node_queued_ops.data/params`,
  `idempotency_records.body` (its key is a SHA-256 of user, action and request id).
* Key: `SUI_SECRET_KEY` (base64 of 32 bytes) or `<db dir>/secret.key`, created
  on first start. Without a key nothing is sealed (and nothing breaks).
* Plaintext rows from older releases are sealed on start; they still read
  correctly before that.
* Downgrade: `3sui secrets -decrypt` writes the secrets back as plaintext.

## mTLS (optional)

1. Create a CA and a client certificate for the master (any PKI).
2. Node: *Settings → Interface → Master client CA* = the CA's PEM. The panel
   must terminate TLS itself; apiv2 then answers only verified clients
   (403 otherwise). The browser UI does not need a certificate.
3. Master: node drawer → *Client certificate* / *Client key* (key write-only,
   sealed). The pair is checked on save.

## Limitations

* mTLS does not work behind a TLS-terminating proxy (fail closed).
* A database moved without its key reads node tokens as empty: re-enter them.
* The rate limit is per master process; the breaker and idempotency records
  survive restarts (stored in the database).

## Logging

Logs never contain tokens or payloads: remote operations are logged as
`op on node #id by actor: status [request id]`.
