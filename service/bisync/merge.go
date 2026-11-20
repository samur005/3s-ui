package bisync

import "strings"

// MergeRemote decides how a remote record interacts with the local copy.
// localOK false means the UID does not exist locally (or is unknown).
//
// Rules:
//   - echo: remote.Origin == localPanelID → ignore (we originated it)
//   - no local: apply remote (create or revive)
//   - remote tombstone, local alive: tombstone if remote.Rev > local.Base path;
//     if local also edited (local.Rev > remote.Base && remote.Rev > local.Base) → conflict
//   - both alive, same payload identity via rev chain:
//       remote.Base == local.Rev → fast-forward apply
//       local.Base == remote.Rev → noop (we are ahead; push later)
//       otherwise → conflict (divergent edits)
func MergeRemote(localPanelID string, localOK bool, local, remote Record) MergeResult {
	remote.UID = strings.TrimSpace(remote.UID)
	if remote.UID == "" {
		return MergeResult{Action: "noop"}
	}
	if remote.Origin != "" && remote.Origin == localPanelID {
		return MergeResult{Action: "echo", Record: remote}
	}
	if !localOK {
		if remote.DeletedAt > 0 {
			// Never seen live; store tombstone so we do not recreate from stale peers.
			return MergeResult{Action: "tombstone", Record: remote}
		}
		return MergeResult{Action: "apply", Record: remote}
	}

	// Identical tip
	if local.Rev == remote.Rev && local.Origin == remote.Origin && local.DeletedAt == remote.DeletedAt {
		return MergeResult{Action: "noop", Record: local}
	}

	// Fast-forward: remote built on our tip
	if remote.Base == local.Rev && remote.Rev > local.Rev {
		if remote.DeletedAt > 0 {
			return MergeResult{Action: "tombstone", Record: remote}
		}
		return MergeResult{Action: "apply", Record: remote}
	}

	// We are strictly ahead of what remote based on
	if local.Base == remote.Rev && local.Rev > remote.Rev {
		return MergeResult{Action: "noop", Record: local}
	}

	// Concurrent divergence
	c := &Conflict{
		UID:       remote.UID,
		Kind:      remote.Kind,
		Local:     local,
		Remote:    remote,
		CreatedAt: NowUnix(),
	}
	return MergeResult{Action: "conflict", Conflict: c, Record: local}
}

// Resolve applies an operator decision to a conflict.
func Resolve(c Conflict, d Decision, localPanelID string) (MergeResult, bool) {
	switch d {
	case KeepLocal:
		// Bump local as authoritative tip over remote.
		out := c.Local
		out.Base = c.Remote.Rev
		if out.Rev <= c.Remote.Rev {
			out.Rev = c.Remote.Rev + 1
		}
		out.Origin = localPanelID
		return MergeResult{Action: "apply", Record: out}, true
	case KeepRemote:
		out := c.Remote
		out.Base = c.Local.Rev
		if out.Rev <= c.Local.Rev {
			out.Rev = c.Local.Rev + 1
		}
		// Keep remote origin; we accept their content.
		return MergeResult{Action: "apply", Record: out}, true
	default:
		return MergeResult{Action: "noop"}, false
	}
}

// BumpRev returns a new rev/base/origin for a local edit on top of current.
func BumpRev(cur Record, localPanelID string) Record {
	out := cur
	out.Base = cur.Rev
	out.Rev = cur.Rev + 1
	if out.Rev < 1 {
		out.Rev = 1
	}
	out.Origin = localPanelID
	out.DeletedAt = 0
	return out
}

// Tombstone marks a local delete.
func Tombstone(cur Record, localPanelID string) Record {
	out := BumpRev(cur, localPanelID)
	out.DeletedAt = NowUnix()
	out.Payload = nil
	return out
}
