# Upgrade

Order does not matter: an upgraded master manages old nodes at API level 1, and
an old master keeps working with upgraded nodes (they answer every legacy
action as before; the new headers are optional).

## Master

1. Back up: *Panel Settings → Backup* (now includes `sync_conflicts` and
   `node_operations`) or copy the database file.
2. Install the new release the usual way (`install.sh` / Docker image) and
   restart.
3. On start the database is migrated automatically:
   * new columns on `nodes`: `node_group`, `location`, `allow_private`,
     `sub_exclude`, `capabilities`, `caps_at` (additive);
   * new table `node_operations`;
   * one-shot migration `migratedNodeAllowPrivate`: existing nodes get
     *Allow private address* = on, so nodes on LAN/Tailscale/loopback keep
     working. Review it per node and switch it off for public nodes.
   Migrations are idempotent (re-running is a no-op) and remove nothing.
4. Open **Nodes**: health and API level appear within seconds.

## Remote node

1. Back up the node's database.
2. Install the new release and restart. No configuration change is needed.
3. The master detects API level 2 on the next capability refresh (on version
   change) — or press *Refresh capabilities*.

## Rollback

The added columns and table are ignored by older releases; restoring the old
binary works without a database restore. Settings rows (`migrated…`) are
harmless.
