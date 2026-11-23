# Subscription profile headers

3S-UI sends the same subscription response headers as LucX. The list was
taken from LucX's source (`internal/sub/controller.go`: `ApplyCommonHeaders`,
`applyHwidHeaders`, Content-Disposition; `internal/sub/happ.go`:
`ApplyHappHeaders`; `placeholders.go`; `remote_routing.go`) and reimplemented
— nothing was copied. They apply to every format (links, `?format=json`,
`?format=clash`) and to HEAD; the dashboard (`?format=page`) gets the common
ones with `Cache-Control: no-cache`.

Settings: **Panel Settings → Profile headers**. Empty = header not sent.
Values are validated on save (no CR/LF, URLs must be http(s), enums).

## Common

| Header | Setting | Notes |
|---|---|---|
| `Subscription-Userinfo` | (existing) | upload/download/total/expire |
| `Profile-Update-Interval` | (existing *Automatic Update Time*) | hours |
| `Profile-Title` | `subTitle` | default: client name/remark as before; non-ASCII is sent as `base64:…`; also renames the Content-Disposition file |
| `Support-Url` | `subSupportUrl` | |
| `Profile-Web-Page-Url` | `subProfileMode` (`none` / `builtin` / `custom`), `subProfileUrl` | `builtin` = this client's dashboard URL |
| `Announce` | `subAnnounce` | always `base64:` (multi-line allowed) |
| `Routing-Enable` | `subEnableRouting` | `true`; `0` for a Happ client when off |
| `Routing` | `subRoutingRules` | routing JSON → `happ://routing/onadd/<base64>`; a `happ://` link as is; an https URL is fetched (10 min cache, last good answer kept on errors, SSRF-guarded, 16 KiB cap) |
| `Hide-Settings` | `subHideSettings` | `1`; `0` for a Happ client when off |
| Incy line in the body | `subIncyEnableRouting`, `subIncyRoutingRules` | `incy://autorouting/onadd/<https url>` or an `incy://` link appended to the plain link list |
| `Content-Disposition` | — | attachment name from `subTitle` |
| custom | `subCustomHeaders` (existing) | e.g. `Announce-Url` (not emitted by LucX itself) |

## Device limit (X-HWID)

Per client: **Device limit (HWID)** in the client form (`limitHwid`, 0 = off).
Devices are stored hashed (SHA-256) with app/OS metadata from `User-Agent`,
`X-Device-OS`, `X-Ver-OS`, `X-Device-Model`; the client form lists them and can
free one or all slots.

| Header | When |
|---|---|
| `X-Hwid-Active: true` | the client has a limit |
| `X-Hwid-Not-Supported: true` | limit set but no usable `X-HWID` (served anyway, like LucX) |
| `X-Hwid-Limit: true` | all slots used |
| `X-Hwid-Max-Devices-Reached: true` | a new device was refused — answer is 404 |

The dashboard and `?format=info` stay reachable for a refused device.

## Happ only

Sent only when `subHappAutoDetect` is on and the User-Agent matches `\bhapp\b`.

| Header | Setting |
|---|---|
| `ProviderID` | `subHappProviderId` |
| `New-Url` / `Fallback-Url` | `subHappNewUrl` / `subHappFallbackUrl` |
| `Sub-Info-Text`, `Sub-Info-Color`, `Sub-Info-Button-Text`, `Sub-Info-Button-Link` | `subHappSubInfoText` (required for the group), `…Color` (blue/green/red), `…ButtonText`, `…ButtonLink` |
| `Sub-Expire` (`1`), `Sub-Expire-Button-Link` | `subHappSubExpire`, `subHappSubExpireButtonLink` |
| `Notification-Subs-Expire` (`1`) | `subHappNotificationExpire` |
| `No-Limit-Enabled` (`1`) | `subHappNoLimit` |
| `Subscription-Always-Hwid-Enable` (`1`) | `subHappAlwaysHwid` |
| `Tun-Mode`, `Tun-Type` | `subHappTunMode`, `subHappTunType` |
| `Exclude-Routes` | `subHappExcludeRoutes` |
| `Exclude-Apns-Enable` (`true`) | `subHappExcludeApns` |
| `Color-Profile` | `subHappColorProfile` |
| `Ping-Type` | `subHappPingType` (`http` is sent as `proxy`) |
| `Subscription-Autoconnect` (`1`), `Subscription-Autoconnect-Type` | `subHappAutoConnect`, `subHappAutoConnectType` (`lowestdelay`/`lastused`) |
| `Socks-Auth-Mode`, `Http-Auth-Mode` | `subHappLocalProxyAuth` |
| `Per-App-Proxy-Mode`, `Per-App-Proxy-List` | `subHappPerAppMode` (`off`/`on`/`bypass`), `subHappPerAppList` |

## Placeholders (per-client values)

In the title, URLs and announcement: `{{USERNAME}}`, `{{EMAIL}}`, `{{SUB_ID}}`,
`{{NAME}}` (client name — 3S-UI's subscription id), `{{ID}}`, `{{SHORT_ID}}`
(first 8 chars of the name), `{{TELEGRAM_ID}}`, plus `{{REMARK}}` and
`{{GROUP}}`. In URLs they are URL-escaped. LucX has no other per-client header
overrides besides the device limit.
