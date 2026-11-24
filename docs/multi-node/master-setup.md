# Master setup

The master is an ordinary 3S-UI panel. Nothing is installed on it beyond the
release that contains this feature.

1. Create an API token **on the node** (see [remote-setup.md](remote-setup.md)).
2. On the master open **Nodes → Add** and fill in:
   * **Name** — unique; it prefixes the node's links in subscriptions (`[name] …`).
   * **Panel URL** — `scheme://host[:port]` only (no path, query, credentials).
     Prefer `https://`. The panel path goes into **Web path** (default `/app/`).
   * **API token** — write-only: the master never returns it; leave the field
     empty when editing to keep the stored one.
   * TLS: verified by default; for a self-signed node set **Certificate pin
     (SHA-256)** rather than *Skip TLS verification*.
   * **Group** / **Location** — free text (≤ 64 chars) for filtering and bulk ops.
   * **Allow private address** — required when the node is on loopback, a LAN
     (RFC 1918 / ULA) or CGNAT (100.64/10, Tailscale) address. Link-local and
     cloud-metadata addresses (169.254.0.0/16, fe80::/10) are always refused.
     Existing nodes were switched on by the upgrade so they keep working.
   * **Exclude from subscriptions** — hide this node's servers from every
     subscription without deleting anything.
   * **Sync mode** — one-way (default) or bidirectional (see
     [synchronization.md](synchronization.md)).
3. **Test connection** shows state, latency, versions and API level.
4. Save. Within 5 s the heartbeat marks the node; capabilities are fetched in
   the background.

## Operating

* **Nodes page** — health chip (online / degraded / syncing / incompatible /
  core stopped / offline / disabled; hover for reasons), group and location,
  filters by group and state, **Select** for bulk actions (each confirmed):
  enable, disable, reconnect, sync now, refresh capabilities, exclude/include in
  subscriptions, set group.
* **Details (eye icon)** — `/app/node?id=N` with tabs Overview, Inbounds,
  Outbounds (+ Endpoints), Clients, Routing, DNS, Capabilities, Traffic,
  Services, Logs, Settings, Audit / Sync.
* **Inbounds on a node from the master's form** — Inbounds → Add → *Target*:
  pick a node instead of *Local panel*. The inbound is written on the node
  (its validators; the port is checked there first; TLS list = the node's) and
  adopted at once as a replica, with the picked clients bound and pushed.
  A replica's *Edit* opens the node's inbound and saves it there (no tag
  change); its delete can also delete it on the node (default) or only remove
  the replica. API: `nodeInboundSave`, `nodeInboundForm` ([api.md](api.md)).
* **Clients** — the client form's inbound list is one searchable dropdown
  grouped *Master* / `[node]`; saving reconciles affected nodes at once (a
  change during a running sync queues one more right after it).
* **Token rotation** — node drawer or Settings tab: the node issues a new token,
  the master verifies and stores it, then revokes the old one (API level 2 nodes).

Deleting a node is refused while the master still has adopted replicas of its
inbounds — remove them on the Inbounds page first.
