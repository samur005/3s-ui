package qwdtt

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// coreFP is the fingerprint of the core fragment the running core was built
// with; see CoreNeedsRestart.
var (
	coreMu sync.Mutex
	coreFP string
	coreOn bool
)

// coreFragment describes what the panel adds to the core config for cfg.
func coreFragment(cfg Config) (tun json.RawMessage, rules []json.RawMessage, active bool) {
	if !RoutingSupported() || !cfg.Enabled || !cfg.RouteThroughCore {
		return nil, nil, false
	}
	tunObj := map[string]any{
		"type":           "tun",
		"tag":            TunTag,
		"interface_name": TunName,
		"address":        []string{TunAddress},
		"mtu":            TunMTU,
		// The panel does the policy routing for the server's interfaces itself;
		// auto_route would take over the host's default route.
		"auto_route": false,
		"stack":      "system",
	}
	tun, _ = json.Marshal(tunObj)
	// Let domain rules see through the tunnel.
	sniff, _ := json.Marshal(map[string]any{"inbound": []string{TunTag}, "action": "sniff"})
	rules = append(rules, sniff)
	if tag := strings.TrimSpace(cfg.OutboundTag); tag != "" {
		force, _ := json.Marshal(map[string]any{"inbound": []string{TunTag}, "action": "route", "outbound": tag})
		rules = append(rules, force)
	}
	return tun, rules, true
}

func fingerprint(cfg Config) string {
	tun, rules, active := coreFragment(cfg)
	if !active {
		return ""
	}
	var b strings.Builder
	b.Write(tun)
	for _, r := range rules {
		b.Write(r)
	}
	return b.String()
}

// InjectCore adds the qWDTT TUN inbound and its route rules to a core config
// that is about to start. It is a no-op while qWDTT is off, not routed through
// the core, or unsupported. A failure to read the stored config leaves the
// core config as it was: the panel must start whatever happens here.
func InjectCore(inbounds []json.RawMessage, route json.RawMessage) ([]json.RawMessage, json.RawMessage, error) {
	cfg, err := Load()
	if err != nil {
		return inbounds, route, nil
	}
	return injectInto(cfg, inbounds, route)
}

func injectInto(cfg Config, inbounds []json.RawMessage, route json.RawMessage) ([]json.RawMessage, json.RawMessage, error) {
	tun, rules, active := coreFragment(cfg)
	coreMu.Lock()
	coreOn = active
	coreFP = fingerprint(cfg)
	coreMu.Unlock()
	if !active {
		return inbounds, route, nil
	}
	// An operator-made inbound with the same tag or interface name would stop
	// the core from starting; step aside rather than break the whole panel.
	for _, raw := range inbounds {
		var probe struct {
			Tag  string `json:"tag"`
			Name string `json:"interface_name"`
		}
		if json.Unmarshal(raw, &probe) == nil && (probe.Tag == TunTag || probe.Name == TunName) {
			return inbounds, route, fmt.Errorf("qwdtt: an inbound already uses tag %q or interface %q", TunTag, TunName)
		}
	}
	out := make([]json.RawMessage, 0, len(inbounds)+1)
	out = append(out, inbounds...)
	out = append(out, tun)

	r := map[string]json.RawMessage{}
	if len(route) > 0 && string(route) != "null" {
		if err := json.Unmarshal(route, &r); err != nil {
			return inbounds, route, fmt.Errorf("qwdtt: route section: %w", err)
		}
	}
	var existing []json.RawMessage
	if raw, ok := r["rules"]; ok {
		_ = json.Unmarshal(raw, &existing)
	}
	// First, so the forced outbound beats the operator's rules, and sniffing
	// has run before any of them is evaluated. Rules the operator wrote for
	// other inbounds are untouched behind it.
	merged := append(append([]json.RawMessage{}, rules...), existing...)
	r["rules"], _ = json.Marshal(merged)
	newRoute, err := json.Marshal(r)
	if err != nil {
		return inbounds, route, err
	}
	return out, newRoute, nil
}

// CoreNeedsRestart reports whether the core has to be restarted for cfg: the
// running core was built with a different qWDTT fragment than cfg implies.
func CoreNeedsRestart(cfg Config) bool {
	coreMu.Lock()
	defer coreMu.Unlock()
	return coreFP != fingerprint(cfg)
}
