package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"

	"github.com/samur005/3s-ui/util/secretbox"
)

type Setting struct {
	Id    uint   `json:"id" form:"id" gorm:"primaryKey;autoIncrement"`
	Key   string `json:"key" form:"key"`
	Value string `json:"value" form:"value"`
}

type Tls struct {
	Id     uint            `json:"id" form:"id" gorm:"primaryKey;autoIncrement"`
	Name   string          `json:"name" form:"name"`
	Server json.RawMessage `json:"server" form:"server"`
	Client json.RawMessage `json:"client" form:"client"`
}

type User struct {
	Id         uint   `json:"id" form:"id" gorm:"primaryKey;autoIncrement"`
	Username   string `json:"username" form:"username"`
	Password   string `json:"password" form:"password"`
	LastLogins string `json:"lastLogin"`
	// Base32 TOTP shared secret; empty means the second factor is off. Tagged
	// json:"-" because knowing it is enough to generate valid codes: the panel
	// hands it out exactly once, during enrolment, and never reads it back to
	// any caller afterwards.
	TwoFaSecret string `json:"-"`
	// Counter the last accepted code matched. A code stays valid for up to 90
	// seconds, so without this the same six digits could be replayed for the
	// rest of that window; anything at or below it is refused.
	TwoFaCounter int64 `json:"-"`
	// Whether TwoFaSecret is set, which is all the UI is ever told. It is
	// computed by the query that selects it, so it is read-only ("->") and
	// excluded from migrations ("-:migration") -- there is no such column.
	TwoFa bool `json:"twoFa" gorm:"->;-:migration"`
}

type LoginAttempt struct {
	Id    uint   `json:"id" gorm:"primaryKey;autoIncrement"`
	Scope string `json:"scope" gorm:"uniqueIndex:idx_login_attempt,priority:1"`
	Key   string `json:"key" gorm:"uniqueIndex:idx_login_attempt,priority:2"`
	// Failures counted since WindowStart. After a username ban is served,
	// Failures stays zero and WindowStart tracks the latest continued attack so
	// the username scope can rearm after a quiet period. Times are unix seconds.
	Failures    int   `json:"failures"`
	WindowStart int64 `json:"windowStart"`
	// Unix seconds until which logins are refused. A served username ban keeps
	// its old positive value while the row is disarmed; loginBanRemaining is the
	// authority on whether it is active. Wall-clock on purpose, like the IP
	// limit's bans: outliving the process is the whole point.
	BannedUntil int64 `json:"bannedUntil"`
}

type Client struct {
	Id       uint            `json:"id" form:"id" gorm:"primaryKey;autoIncrement"`
	Enable   bool            `json:"enable" form:"enable"`
	Name     string          `json:"name" form:"name"`
	// SubId is the stable subscription path segment. It never changes when
	// the client is renamed, so a shared link keeps working. Empty on rows
	// created before the field existed; filled on the next save.
	SubId    string          `json:"subId,omitempty" form:"subId" gorm:"index;default:'';size:36"`
	Config   json.RawMessage `json:"config,omitempty" form:"config"`
	Inbounds json.RawMessage `json:"inbounds" form:"inbounds"`
	Links    json.RawMessage `json:"links,omitempty" form:"links"`
	Volume   int64           `json:"volume" form:"volume"`
	Expiry   int64           `json:"expiry" form:"expiry"`
	Down     int64           `json:"down" form:"down"`
	Up       int64           `json:"up" form:"up"`
	Desc     string          `json:"desc" form:"desc"`
	Group    string          `json:"group" form:"group"`
	Remark   string          `json:"remark" form:"remark"`

	// Cluster bi-di identity (empty = not enrolled in bidirectional sync).
	SyncUID    string `json:"syncUid,omitempty" form:"syncUid" gorm:"index;default:'';size:36"`
	SyncRev    int64  `json:"syncRev,omitempty" form:"syncRev" gorm:"default:0;not null"`
	SyncBase   int64  `json:"syncBase,omitempty" form:"syncBase" gorm:"default:0;not null"`
	SyncOrigin string `json:"syncOrigin,omitempty" form:"syncOrigin" gorm:"default:'';size:64"`
	DeletedAt  int64  `json:"deletedAt,omitempty" form:"deletedAt" gorm:"default:0;not null;index"`

	// Timestamps (unix seconds): creation time and last time the client had traffic
	CreatedAt int64 `json:"createdAt" form:"createdAt" gorm:"default:0;not null"`
	OnlineAt  int64 `json:"onlineAt" form:"onlineAt" gorm:"default:0;not null"`

	// Cap on concurrently connected source IPs; 0 = unlimited. Enforced by the
	// panel's own periodic scan of the connection tracker, not by sing-box.
	// Indexed because the scan runs every 10s asking for the rows above zero,
	// which are the rare ones -- exactly the shape an index answers cheaply.
	LimitIp int `json:"limitIp" form:"limitIp" gorm:"default:0;not null;index"`

	// Cap on devices (distinct X-HWID values) that may fetch the
	// subscription; 0 = no device limit (sub/hwid.go).
	LimitHwid int `json:"limitHwid" form:"limitHwid" gorm:"default:0;not null"`

	// Telegram user allowed to look this client up in the bot; 0 = nobody.
	// Bound through the bot, so the panel's own client form does not carry it
	// and ClientService.preserveServerManagedFields keeps an edit from writing
	// it back to zero. Indexed because every incoming bot message resolves the
	// sender through it.
	TgId int64 `json:"tgId" form:"tgId" gorm:"default:0;not null;index"`

	// Delay start and periodic reset. Delay start only holds the reset clock
	// until the client's first bytes, so it means nothing without auto reset,
	// and every write path clears the two together.
	DelayStart bool `json:"delayStart" form:"delayStart" gorm:"default:false;not null"`
	AutoReset  bool `json:"autoReset" form:"autoReset" gorm:"default:false;not null"`

	// Days between periodic resets, or zero when ResetDayOfMonth supplies the
	// schedule instead.
	ResetDays int `json:"resetDays" form:"resetDays" gorm:"default:0;not null"`

	// Day of the month the periodic reset lands on, 1-31; 0 keeps the plain
	// ResetDays period. It is stored rather than derived from NextReset because
	// a short month has to clamp -- 31 in February means the 28th -- and a
	// clamped date cannot be the anchor for the next one, or the day would walk
	// backwards to the 28th and stay there. Every boundary is recomputed from
	// this number instead. Not a cron spec for the same reason: cron matches the
	// day exactly, so `0 0 31 * *` skips February, April, June, September and
	// November outright -- five months with no reset at all.
	ResetDayOfMonth int `json:"resetDayOfMonth" form:"resetDayOfMonth" gorm:"default:0;not null"`

	NextReset int64 `json:"nextReset" form:"nextReset" gorm:"default:0;not null"`
	TotalUp   int64 `json:"totalUp" form:"totalUp" gorm:"default:0;not null"`
	TotalDown int64 `json:"totalDown" form:"totalDown" gorm:"default:0;not null"`
}

// MarshalJSON writes a client whose stored JSON columns do not hold JSON as
// one that has none: config and links left out, inbounds an empty list.
//
// encoding/json refuses a json.RawMessage that is not valid JSON, and the
// refusal is the whole document's. One damaged row -- written by hand, or
// restored from a broken backup -- made the client list, api/load and every
// save response fail to encode, and gin answers that with a 200 and an empty
// body, so the panel showed no clients at all and said nothing. Written this
// way the row stays in the list, where it can be seen, fixed or deleted;
// reportUnreadableClients names it in the log at start.
func (c Client) MarshalJSON() ([]byte, error) {
	type plain Client
	p := plain(c)
	if !validOrEmpty(p.Config) {
		p.Config = nil
	}
	if !validOrEmpty(p.Inbounds) {
		p.Inbounds = json.RawMessage("[]")
	}
	if !validOrEmpty(p.Links) {
		p.Links = nil
	}
	return json.Marshal(p)
}

func validOrEmpty(raw json.RawMessage) bool {
	return len(raw) == 0 || json.Valid(raw)
}

type Stats struct {
	Id uint64 `json:"id" gorm:"primaryKey;autoIncrement"`
	// date_time sits third in idx_stats_bucket, so that index cannot serve the
	// retention purge, which filters on date_time alone and scans the table
	// instead.
	DateTime  int64  `json:"dateTime" gorm:"uniqueIndex:idx_stats_bucket,priority:3;index:idx_stats_date_time"`
	Resource  string `json:"resource" gorm:"uniqueIndex:idx_stats_bucket,priority:1"`
	Tag       string `json:"tag" gorm:"uniqueIndex:idx_stats_bucket,priority:2"`
	Direction bool   `json:"direction" gorm:"uniqueIndex:idx_stats_bucket,priority:4"`
	Traffic   int64  `json:"traffic"`
}

type TargetStats struct {
	Id       uint64 `json:"id" gorm:"primaryKey;autoIncrement"`
	DateTime int64  `json:"dateTime" gorm:"uniqueIndex:idx_target_stats_bucket,priority:3;index:idx_target_stats_date_time"`
	User     string `json:"user" gorm:"uniqueIndex:idx_target_stats_bucket,priority:1;size:100;default:''"`
	Target   string `json:"target" gorm:"uniqueIndex:idx_target_stats_bucket,priority:2;size:255"`
	Up       int64  `json:"up" gorm:"default:0"`
	Down     int64  `json:"down" gorm:"default:0"`
}

type Changes struct {
	Id       uint64          `json:"id" gorm:"primaryKey;autoIncrement"`
	DateTime int64           `json:"dateTime"`
	Actor    string          `json:"actor"`
	Key      string          `json:"key"`
	Action   string          `json:"action"`
	Obj      json.RawMessage `json:"obj"`
}

type Tokens struct {
	Id     uint   `json:"id" form:"id" gorm:"primaryKey;autoIncrement"`
	Desc   string `json:"desc" form:"desc"`
	Token  string `json:"token" form:"token"`
	Expiry int64  `json:"expiry" form:"expiry"`
	UserId uint   `json:"userId" form:"userId"`
	User   *User  `json:"user" gorm:"foreignKey:UserId;references:Id"`
}

// Node is another 3s-ui panel managed by this one over its apiv2 (Token
// header). Only connection config and the last observed transition live in
// the DB — live status is kept in an in-memory snapshot (service/node.go).
type Node struct {
	Id       uint         `json:"id" form:"id" gorm:"primaryKey;autoIncrement"`
	Enable   bool         `json:"enable" form:"enable" gorm:"default:true;not null"`
	Name     string       `json:"name" form:"name" gorm:"unique"`
	BaseUrl  string       `json:"baseUrl" form:"baseUrl"` // scheme://host[:port], no path
	WebPath  string       `json:"webPath" form:"webPath"` // remote panel web path, default "/app/"
	Token    SecretString `json:"token,omitempty" form:"token"`
	Insecure bool         `json:"insecure" form:"insecure" gorm:"default:false;not null"`
	CertPin  string       `json:"certPin" form:"certPin"` // leaf cert SHA-256, overrides Insecure
	Desc     string       `json:"desc" form:"desc"`
	// Unix seconds of the last time the node was seen online; written only on
	// online -> offline/core-stopped transitions, not every heartbeat.
	LastSeen int64 `json:"lastSeen" form:"lastSeen" gorm:"default:0;not null"`

	// Dirty marks pending master-side edits that have not converged onto the
	// node yet; the heartbeat retriggers Reconcile while it is set.
	Dirty    bool  `json:"dirty" gorm:"default:false;not null"`
	LastSync int64 `json:"lastSync" gorm:"default:0;not null"`
	// SyncMode is "one_way" (default) or "bidirectional". Never auto-enabled.
	SyncMode string `json:"syncMode" form:"syncMode" gorm:"default:one_way;not null;size:32"`
	// Baselines holds the per-client traffic counters seen on the node at the
	// last collection: map[clientName]{up,down}. Single writer (traffic job),
	// one row UPDATE per node per cycle.
	Baselines json.RawMessage `json:"-"`

	// ---- Multi-node management (added with node API level 2) ----
	// NodeGroup is an operator label for grouping nodes and bulk operations
	// (e.g. "eu", "backup"). Free text, not a cluster scope: client sync stays
	// scoped by the @cluster client group regardless of it.
	NodeGroup string `json:"group" form:"group" gorm:"column:node_group;size:64;index;default:'';not null"`
	// Location is display-only (city / country / provider).
	Location string `json:"location" form:"location" gorm:"size:64;default:'';not null"`
	// AllowPrivate lets the master dial a node whose address resolves to a
	// loopback or private (RFC 1918 / ULA) address. Link-local, multicast and
	// unspecified addresses are refused regardless (cloud metadata endpoints
	// live there). Existing nodes are migrated to true so an upgrade does not
	// cut off a cluster on a private network (database.migrateNodeAllowPrivate).
	AllowPrivate bool `json:"allowPrivate" form:"allowPrivate" gorm:"default:false;not null"`
	// SubExclude keeps the node managed and monitored but leaves its servers
	// out of every master subscription. The stored links are kept, so turning
	// it back off restores them without a reconcile.
	SubExclude bool `json:"subExclude" form:"subExclude" gorm:"default:false;not null"`
	// Capabilities is the node's last nodeCapabilities answer (JSON), CapsAt
	// when it was fetched. Heartbeat-owned: never taken from a form.
	Capabilities json.RawMessage `json:"-"`
	CapsAt       int64           `json:"capsAt" gorm:"default:0;not null"`

	// Optional mutual TLS: the client certificate (PEM) the master presents
	// to this node, and its private key (PEM, write-only, sealed at rest).
	// Used alongside CertPin / normal verification, never instead of it.
	ClientCert string       `json:"clientCert" form:"clientCert" gorm:"type:text;default:'';not null"`
	ClientKey  SecretString `json:"clientKey,omitempty" form:"clientKey" gorm:"type:text;default:'';not null"`

	// Persisted circuit-breaker state, so a restart does not hammer a node
	// that was failing (service/nodeHealth.go). Heartbeat-owned.
	BreakerFails int   `json:"-" gorm:"default:0;not null"`
	BreakerUntil int64 `json:"-" gorm:"default:0;not null"`
}

// SecretString is a string sealed with util/secretbox when written to the
// database and opened when read, so the rest of the code handles plaintext
// and the database never holds it (Node.Token, Node.ClientKey). Legacy
// plaintext rows read as they are (database.migrateSealSecrets seals them).
// A value that cannot be opened (database moved without its secret.key)
// reads as empty: the node then shows "check the API token" and the operator
// re-enters it, rather than ciphertext being sent as a token.
type SecretString string

func (s SecretString) Value() (driver.Value, error) {
	return secretbox.Seal(string(s))
}

func (s *SecretString) Scan(src any) error {
	var raw string
	switch v := src.(type) {
	case nil:
		raw = ""
	case string:
		raw = v
	case []byte:
		raw = string(v)
	default:
		return fmt.Errorf("SecretString: unsupported type %T", src)
	}
	if v, err := secretbox.Open(raw); err == nil {
		*s = SecretString(v)
	} else {
		*s = ""
	}
	return nil
}

func (s SecretString) String() string { return string(s) }

// SealNodeSecret seals a value for a direct column update.
func SealNodeSecret(v string) (string, error) { return secretbox.Seal(v) }

// ClientHwid is one device (hashed X-HWID) seen fetching a client's
// subscription; Client.LimitHwid caps how many may register.
type ClientHwid struct {
	Id          uint   `json:"id" gorm:"primaryKey;autoIncrement"`
	ClientId    uint   `json:"clientId" gorm:"uniqueIndex:idx_client_hwid;not null"`
	HwidHash    string `json:"hwidHash" gorm:"size:64;uniqueIndex:idx_client_hwid;not null"`
	FirstSeen   int64  `json:"firstSeen" gorm:"not null"`
	LastSeen    int64  `json:"lastSeen" gorm:"not null"`
	UserAgent   string `json:"userAgent" gorm:"size:512;default:'';not null"`
	DeviceOS    string `json:"deviceOs" gorm:"size:128;default:'';not null"`
	OsVersion   string `json:"osVersion" gorm:"size:128;default:'';not null"`
	DeviceModel string `json:"deviceModel" gorm:"size:128;default:'';not null"`
}

// IdempotencyRecord persists the answer to an apiv2 write that carried an
// X-Request-Id (api/idempotency.go), so a master retrying after a node
// restart still gets the replay instead of a second execution. Body is sealed.
type IdempotencyRecord struct {
	Key       string `gorm:"primaryKey;size:200"`
	Status    int    `gorm:"not null"`
	Body      string `gorm:"type:text;not null"`
	CreatedAt int64  `gorm:"not null"`
	ExpiresAt int64  `gorm:"index;not null"`
}

// NodeQueuedOp is a remote write that could not reach its node and was queued
// to run when the node is back (service/nodeQueue.go). Data/Params are sealed:
// they can carry inbound credentials.
type NodeQueuedOp struct {
	Id        uint64 `json:"id" gorm:"primaryKey;autoIncrement"`
	NodeId    uint   `json:"nodeId" gorm:"index;not null"`
	Op        string `json:"op" gorm:"size:32;not null"`
	Action    string `json:"action" gorm:"size:16;default:'';not null"`
	Data      string `json:"-" gorm:"type:text;default:'';not null"`
	Params    string `json:"-" gorm:"type:text;default:'';not null"`
	Actor     string `json:"actor" gorm:"size:64;default:'';not null"`
	RequestId string `json:"requestId" gorm:"size:64;not null"`
	Status    string `json:"status" gorm:"size:16;index;not null"` // pending | done | failed | cancelled
	Attempts  int    `json:"attempts" gorm:"default:0;not null"`
	NextAt    int64  `json:"nextAt" gorm:"index;not null"`
	LastError string `json:"lastError" gorm:"size:512;default:'';not null"`
	CreatedAt int64  `json:"createdAt" gorm:"not null"`
	UpdatedAt int64  `json:"updatedAt" gorm:"not null"`
}

// NodeOperation is the master-side audit trail of typed remote operations
// (service/nodeRemote.go): who ran what against which node, with which request
// id, and how it ended. Payloads are deliberately not stored -- they carry
// credentials (inbound users, private keys) that an audit table must not keep.
type NodeOperation struct {
	Id         uint64 `json:"id" gorm:"primaryKey;autoIncrement"`
	NodeId     uint   `json:"nodeId" gorm:"index;not null"`
	RequestId  string `json:"requestId" gorm:"size:64;uniqueIndex;not null"`
	Op         string `json:"op" gorm:"size:32;not null"`
	Object     string `json:"object" gorm:"size:32;default:'';not null"`
	Action     string `json:"action" gorm:"size:16;default:'';not null"`
	Actor      string `json:"actor" gorm:"size:64;default:'';not null"`
	Status     string `json:"status" gorm:"size:16;not null"` // ok | failed | refused
	Error      string `json:"error" gorm:"size:512;default:'';not null"`
	Attempts   int    `json:"attempts" gorm:"default:1;not null"`
	StartedAt  int64  `json:"startedAt" gorm:"index;not null"`
	DurationMs int64  `json:"durationMs" gorm:"default:0;not null"`
}

// SyncConflict holds a divergent bi-di edit until an operator resolves it.
type SyncConflict struct {
	Id         uint   `json:"id" gorm:"primaryKey;autoIncrement"`
	UID        string `json:"uid" gorm:"index;size:36;not null"`
	Kind       string `json:"kind" gorm:"size:16;not null"` // client|inbound
	NodeId     uint   `json:"nodeId" gorm:"index;default:0"`
	LocalJSON  string `json:"localJson" gorm:"type:text"`
	RemoteJSON string `json:"remoteJson" gorm:"type:text"`
	CreatedAt  int64  `json:"createdAt" gorm:"not null"`
	Resolved   bool   `json:"resolved" gorm:"default:false;not null"`
}
