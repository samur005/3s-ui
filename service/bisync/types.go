package bisync

import "time"

// Sync modes stored on model.Node.SyncMode.
const (
	ModeOneWay        = "one_way"
	ModeBidirectional = "bidirectional"
)

// ProtocolVersion is negotiated in status; peers below this stay one_way.
const ProtocolVersion = 1

// Record is the wire/merge shape for one syncable entity.
type Record struct {
	UID        string         `json:"uid"`
	Kind       string         `json:"kind"` // "client" | "inbound"
	Rev        int64          `json:"rev"`
	Base       int64          `json:"base"`
	Origin     string         `json:"origin"`
	DeletedAt  int64          `json:"deletedAt,omitempty"` // unix seconds; 0 = alive
	Payload    map[string]any `json:"payload,omitempty"`
	NeedsLocal bool           `json:"needsLocal,omitempty"`
}

// Conflict is stored until an operator resolves it.
type Conflict struct {
	UID       string `json:"uid"`
	Kind      string `json:"kind"`
	Local     Record `json:"local"`
	Remote    Record `json:"remote"`
	CreatedAt int64  `json:"createdAt"`
}

// Decision is how an operator resolves a conflict.
type Decision string

const (
	KeepLocal  Decision = "local"
	KeepRemote Decision = "remote"
	Defer      Decision = "defer"
)

// MergeResult is the outcome of merging one remote record into local state.
type MergeResult struct {
	Action   string    // "noop" | "apply" | "conflict" | "tombstone" | "echo"
	Record   Record    // record to apply locally when Action is apply/tombstone
	Conflict *Conflict // set when Action is conflict
}

// NowUnix is overridable in tests.
var NowUnix = func() int64 { return time.Now().Unix() }
