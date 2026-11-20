package bisync

import (
	"encoding/json"
	"strings"
)

// Local-only inbound option keys that must never cross the wire as authoritative.
var inboundLocalOptionKeys = map[string]bool{
	"listen": true, "listen_port": true, "listenAddr": true,
	"interface_name": true, "inet4_address": true, "inet6_address": true,
	"handoffUser": true, "handoffPass": true, "handoffPort": true,
	"private_key": true, "peers": true, "pre_shared_key": true,
	"jc": true, "jmin": true, "jmax": true, // AWG noise can stay in template; keys no
}

// inboundLocalOutKeys are rewritten per node by FillOutJson.
var inboundLocalOutKeys = map[string]bool{
	"server": true, "server_port": true,
}

// ClientPayload extracts syncable client fields from a panel-shaped map.
func ClientPayload(m map[string]any) map[string]any {
	out := map[string]any{}
	for _, k := range []string{"name", "enable", "config", "expiry", "desc", "limitIp", "inboundsSync"} {
		if v, ok := m[k]; ok {
			out[k] = v
		}
	}
	return out
}

// InboundTemplatePayload strips local listen/TLS/addr/secret fields.
func InboundTemplatePayload(m map[string]any) map[string]any {
	out := map[string]any{}
	if v, ok := m["type"]; ok {
		out["type"] = v
	}
	if v, ok := m["tag"]; ok {
		out["tag"] = v
	}
	if v, ok := m["enable"]; ok {
		out["enable"] = v
	}
	if raw, ok := m["options"]; ok {
		out["options"] = stripMap(raw, inboundLocalOptionKeys)
	}
	if raw, ok := m["out_json"]; ok {
		out["out_json"] = stripMap(raw, inboundLocalOutKeys)
	}
	return out
}

func stripMap(raw any, deny map[string]bool) any {
	switch t := raw.(type) {
	case map[string]any:
		dst := map[string]any{}
		for k, v := range t {
			if deny[k] {
				continue
			}
			dst[k] = v
		}
		return dst
	case json.RawMessage:
		var m map[string]any
		if json.Unmarshal(t, &m) != nil {
			return raw
		}
		return stripMap(m, deny)
	default:
		return raw
	}
}

// UnsupportedInboundClone reports protocols that cannot fully clone secrets.
func UnsupportedInboundClone(typ string) bool {
	switch strings.ToLower(strings.TrimSpace(typ)) {
	case "qwdtt", "wg", "awg", "tun", "redirect", "tproxy", "direct", "cloudflared":
		return true
	default:
		return false
	}
}
