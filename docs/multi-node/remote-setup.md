# Remote node setup

A node is a normal 3S-UI install. On the node:

1. Install / upgrade 3S-UI as usual. A node of this release answers API level 2
   (full remote management). Older nodes keep working at level 1 (monitoring,
   sync, subscriptions and basic operations) until upgraded.
2. **Admins → API tokens → Add.** Give it a description such as
   `master <name>` and, if you want, an expiry. Copy the token once; paste it
   into the master's node form.
3. Make the panel reachable from the master only:
   * use HTTPS (ACME in *Panel Settings → Domains & Certificates*), or a
     self-signed certificate pinned on the master;
   * restrict the panel port to the master's IP with the host firewall;
   * if the panel has a web domain configured, the master must use that domain
     (a bare IP gets HTTP 403).
4. Nothing else: no agent, no SSH, no extra port.

## What the master can do on a node

Only the operations listed in the node's `nodeCapabilities.management`
(the same allowlist is enforced on the master):

* read: `hello`, `capabilities`, `health`, `services`, `services.list`, `logs`,
  `inbounds`, `outbounds`, `endpoints`, `clients`, `config`, `tls`,
  `portCheck`, `checkConfig`, `checkOutbound`
* write: `save.inbounds`, `save.outbounds`, `save.endpoints`, `save.services`,
  `config.route`, `config.dns`, `core.restart`, `core.stop`*, `core.start`,
  `panel.restart`*, `token.rotate` (* needs explicit confirmation)

Writes go through the node's own `save` action and validators; the node's
`changes` log records them under the token's admin. A token can be revoked on
the node at any time, which cuts the master off immediately.
