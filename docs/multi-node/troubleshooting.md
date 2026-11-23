# Troubleshooting

| Symptom | Cause / fix |
|---|---|
| `node address refused by the SSRF policy … loopback or private address` | node is on 127.0.0.0/8, RFC 1918, ULA or 100.64/10: enable **Allow private address** on the node (master side) |
| `… link-local, multicast or unspecified address` | the URL resolves to 169.254.x.x / fe80:: / 0.0.0.0 — always refused; use the node's real address |
| `HTTP 403 from node panel` | the node has a web domain configured; use that domain in the Panel URL |
| `invalid token` | token revoked/expired on the node, or rotated elsewhere; create a new one on the node |
| `node redirected to another host` | the URL points at a redirector; use the final address |
| health `degraded` | hover the chip / see Overview → reasons (core stopped, latency, memory/disk ≥ 95 %, sync failures, capabilities, stale metrics) |
| health `incompatible` | the node needs a newer master; upgrade the master |
| `API 1` chip, "runs an older 3S-UI" refusals | upgrade the node for full remote management; monitoring and sync keep working |
| `refused: node X is offline (next probe …)` | breaker open after repeated failures; fix connectivity, then **Reconnect** |
| `too many operations on node X` | per-node rate limit (2/s, burst 20); retry shortly |
| `config check failed, nothing saved` | the node rejected the route/DNS section; the message contains sing-box's error |
| `inbound … is adopted by this master` | remove the replica on the master's Inbounds page first |
| `a request with this X-Request-Id is still running` | a retry overlapped the original; wait and repeat |
| `request timestamp outside the allowed window` | master/node clocks differ by > 15 min: fix NTP |

## End-to-end self-test

`scripts/multinode-e2e/run.sh <3sui binary> [<older 3sui binary>]` starts
throwaway master and node panels on loopback (ports 22095/23095, plus
25095/26095 with an older binary) in a temp directory and runs the checks in
`e2e.py` / `compat.py`. The binary must embed the frontend (copy
`frontend/dist` to `web/html` before `go build`). It touches nothing else.
