# qWDTT (VK TURN proxy) in 3S-UI

3S-UI runs a [qWDTT](https://github.com/SpaceNeuroX/proxy-turn-vk-android)
server (WireGuard over DTLS over the TURN relays of a VK call) as a sidecar,
ties its users to panel clients, keeps VK call hashes alive, and hands out
`qwdtt://` links, including those of nodes, in each client's subscription.

The basics (sidecar, routing through sing-box, VK cookies, the binary) are in
the README, section *qWDTT (VK call tunnel)*. This page covers users, nodes,
hash sources, subscriptions, the API and testing.

## Turning it on

1. **Inbounds → Add → qwdtt.** Pick the clients that should get qWDTT. Leave
   **Users** on *Own password per client* (the default for new inbounds) and
   set **Devices per client** (default 3).
2. Open the firewall: the DTLS port (`56000/udp` by default), and `56003/udp`
   if the raw-IP listener is used.
3. **qWDTT page → VK cookies:** paste the cookies of a VK account, or set a
   **Remote WDTT panel** (below). With *Automatic hashes* on, a hash is
   created at once and kept alive every 5 minutes.
4. Clients find their link under **QR → qWDTT** and in their subscription.

## Users: per client or shared

| Mode | Server side | Link password |
|---|---|---|
| `perClient` (new inbounds) | every bound, active client is an entry in the server's `passwords.json` | the client's own |
| `shared` (inbounds made before this feature) | only the owner `-password` | the inbound's password |

In per-client mode:

* A client's password is `config.qwdtt.password` in the client row. New
  clients get one at creation; existing clients bound to a qWDTT inbound get
  one on the next supervisor tick (`EnsureClientPasswords`). *Shuffle* in the
  client form rotates it.
* Every 10 s the supervisor writes `passwords.json` in the server's config dir
  (default `db/qwdtt`, mode 0600, atomic rename) and sends **SIGHUP** when it
  changed. The server then re-reads it, drops the WireGuard peers of removed
  devices and refreshes its wrap keys.
* The panel owns only entries labelled `3s-ui:<client name>`. Entries made
  some other way (for example the server's Telegram bot) are left untouched
  and listed as *not managed*.
* **Enable / expiry / quota:** a disabled, expired or over-quota client's entry
  is *removed together with its devices*, so its live tunnel stops on the next
  reload. The panel doesn't rely on the server's own `expires_at` or
  `is_deactivated`: the server deletes expired entries itself, and
  `is_deactivated` does not drop peers that are already connected.
* **Devices:** `max_devices` is *Devices per client*, or, when that is 0, the
  client's IP limit (else 1).
* **Traffic:** the server adds each password's bytes into its entry
  (`up_bytes`/`down_bytes`) and saves once a minute. The panel credits the
  difference since the last read to the client (`up`/`down`, `online_at`) and
  to the inbound. Baselines live in the `qwdtt_usages` table; a counter that
  goes down (server reset) is counted from zero.
* The owner password (`-password`) is always passed, since the server refuses
  to start without one. It stays an admin credential and is never put in a
  client link in per-client mode.

## Nodes

A node's qWDTT inbound is adopted on the master like any other node inbound
(type `qwdtt`, replica row with `node_id`). Clients bound to it are pushed to
the node in the `@cluster` group with their whole config, `qwdtt.password`
included, so the node's own supervisor writes them into *its* qWDTT server
exactly as above. Traffic comes back through the normal node traffic
collection.

Links for node servers are built **live** at subscription time from the
replica's options:

* peer: the node inbound's *Share host* (`subHost`), else the node's address
  plus its DTLS port;
* password: the client's own (per-client replica) or the replica's shared one;
* hashes: the node's own, else the master's (a VK hash is not tied to a
  server);
* name: `[<node>] <remark>`.

A node marked *exclude from subscriptions* contributes no link. Replica
options (and so node hashes) are refreshed every 2 minutes
(`qwdtt node replicas` cron job) and after every node reconcile.

## VK hash sources

In order of priority:

1. `S3UI_VK_HASH` (comma-separated, up to 4) pins hashes. Generation is off.
2. **Own VK cookies** stored on the qWDTT page. The panel starts the call
   itself (VK web token → `calls.start`), can end its own calls, and checks
   liveness with `calls.getCallPreview`.
3. **Remote WDTT panel** ([ildarmaga/wdtt](https://github.com/ildarmaga/wdtt)).
   This is the same source LucX uses. The panel logs in at `<url>/login`, takes
   the session and `wdtt-csrf` cookies, and calls
   `POST <url>/panel/api/vk/call/create` with `X-CSRF-Token`. The answer's
   `obj.vk_hash` is the hash. An optional bearer token is sent for a fronting
   proxy. Such calls belong to the WDTT account and are never ended by this
   panel. Set it on the qWDTT page, or in the environment:

   | Variable | LucX alias | Meaning |
   |---|---|---|
   | `S3UI_WDTT_URL` | `LUCX_WDTT_URL` | panel URL incl. its secret path |
   | `S3UI_WDTT_USER` / `S3UI_WDTT_PASS` | `LUCX_WDTT_USER` / `LUCX_WDTT_PASS` | login |
   | `S3UI_WDTT_TOKEN` | `LUCX_WDTT_TOKEN` | bearer token |

   The environment wins over the stored source. Stored secrets are never sent
   back to the browser.

## Subscriptions

* **Plain link list** (`/sub/<client>`): one `qwdtt://config?…` line per qWDTT
  server the client has (master + nodes). This can be switched off under
  *Settings → Subscription → qWDTT links in the subscription* (`qwdttSubLinks`)
  for clients whose apps choke on it. The other links are unaffected.
* **qWDTT app document** (`/sub/<client>?format=qwdtt`, also served for
  `User-Agent: qWDTT-Subscription/…`): the JSON the qWDTT Android/Linux app
  imports as a subscription, with the usual subscription headers:

  ```json
  {"subscriptionName":"alice","description":"3S-UI","trafficUsedMb":12,"trafficLimitMb":0,
   "updatedAt":"2026-11-23","version":1,
   "profiles":[{"name":"qWDTT","peer":"vpn.example.com:56000","hashes":["…"],"workers":16,"port":9000,"password":"…","diagKey":"…"}]}
  ```
* Link parameters follow the server's own `bot_links.go`:
  `name, peer, hashes, workers, port, pass, dk, raw, direct`. `dk` is the
  server's `diag.key` (qWDTT ≥ 1.4.5) in base64url, and is omitted when the
  server has none.

## API

All under the panel's `api/` (session auth as the rest of the panel).

| Method | Path | Purpose |
|---|---|---|
| GET | `qwdtt` | page state: config, status, link, hashes, `remote` (no secrets), `users`, `nodeInbounds` |
| GET | `qwdttUsers` | entries in `passwords.json`: client, managed, maxDevices, devices, up, down (no passwords) |
| GET | `qwdttClient?id=` | a client's links: `[{node, tag, link, perClient}]` |
| GET | `qwdttLogs`, `qwdttSubscription` | server log; admin (owner) subscription JSON |
| POST | `qwdttRemote` | `url, username, password, token`; empty `url` clears, empty secret keeps the stored one |
| POST | `qwdttSave`, `qwdttRestart`, `qwdttCookies`, `qwdttCookiesClear`, `qwdttCookiesCheck`, `qwdttGenerate`, `qwdttHashesCheck` | as before |

## Database

* `qwdtt_states.wdtt`: the stored remote WDTT source (JSON).
* `qwdtt_usages`: `password` (pk), `client_name`, `up`, `down`. The last
  counters read per password.
* `clients.config.qwdtt = {name, password}`.
* qWDTT inbound options gain `userMode` (`perClient`/`shared`) and
  `maxDevices`. A stored inbound without `userMode` reads as `shared`.
* Setting `qwdttSubLinks` (default `true`).

All are created by the normal auto-migration and included in backups.

## Limitations

* **VK TURN itself cannot be tested** outside a real VK call; see *Testing*.
* Up to ~60 s of traffic counted by the server in memory can be lost when the
  panel rewrites `passwords.json` between two of the server's saves (the
  server saves every 60 s and the panel writes only on a user change).
* Removing a client's access drops its device registrations. Turning it back
  on gives the same password, but the app registers its device again.
* One qWDTT inbound per panel (per node).
* The server's admin HTTPS API is not used: it caps passwords at 10 and makes
  them up itself.

## Testing

`go test ./qwdtt ./service ./sub` covers the user sync (ownership, devices,
counter resets, file mode), the traffic credit, the remote WDTT client
(against a fake WDTT panel with CSRF), link building including nodes, and the
qWDTT document.

`scripts/qwdtt-e2e.sh [server-binary]` runs `qwdtt/e2e_linux_test.go`
against the **real** qWDTT server binary, with the panel's supervisor writing
the users. It takes about 90 s and needs sudo. VK's TURN relays are replaced by
a local pion/turn relay, and the real qWDTT client is built with its VK
credential fetch short-circuited to that relay. DTLS, the password handshake,
WireGuard, SIGHUP reload and the traffic counters are all real. It checks
that:

1. a client connects with its own password and fetches 2 MiB;
2. a wrong password is refused;
3. the traffic is credited to the client and the inbound;
4. disabling the client removes it from the server and its tunnel stops;
5. another client still connects.

## Licenses

* qWDTT (server, client, link format): GPL-3.0, SpaceNeuroX. The binary is
  built from source; nothing is vendored.
* WDTT panel API (endpoints, CSRF): GPL-3.0, ildarmaga/wdtt. Only its HTTP
  interface is used.
* LucX's remote-hash idea and environment variable names are followed for
  compatibility. Its code (partly PolyForm Noncommercial) was **not** copied;
  `qwdtt/remote.go` is an independent implementation.
