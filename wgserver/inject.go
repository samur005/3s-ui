package wgserver

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

var (
	coreMu sync.Mutex
	coreFP string
)

// coreFragment describes what the panel adds to the core config for the given
// inbounds: one TUN inbound per enabled inbound that routes through the core,
// and the rules that make domain routing work on it and pin its outbound.
func coreFragment(inbounds []Inbound) (tuns []json.RawMessage, rules []json.RawMessage) {
	if !RoutingSupported() {
		return nil, nil
	}
	var tags []string
	var forced []json.RawMessage
	for _, in := range inbounds {
		if !in.Enable || !in.Config.RouteThroughCore {
			continue
		}
		tun, _ := json.Marshal(map[string]any{
			"type":           "tun",
			"tag":            TunTag(in.ID),
			"interface_name": TunName(in.ID),
			"address":        []string{TunAddress(in.ID)},
			"mtu":            in.Config.EffectiveMTU(in.Type),
			// The panel does the policy routing for the inbound's device itself;
			// auto_route would take over the host's default route.
			"auto_route": false,
			"stack":      "system",
		})
		tuns = append(tuns, tun)
		tags = append(tags, TunTag(in.ID))
		if tag := strings.TrimSpace(in.Config.OutboundTag); tag != "" {
			r, _ := json.Marshal(map[string]any{"inbound": []string{TunTag(in.ID)}, "action": "route", "outbound": tag})
			forced = append(forced, r)
		}
	}
	if len(tuns) == 0 {
		return nil, nil
	}
	// Let domain rules see through the tunnel.
	sniff, _ := json.Marshal(map[string]any{"inbound": tags, "action": "sniff"})
	rules = append([]json.RawMessage{sniff}, forced...)
	return tuns, rules
}

func fingerprint(inbounds []Inbound) string {
	tuns, rules := coreFragment(inbounds)
	var b strings.Builder
	for _, t := range tuns {
		b.Write(t)
	}
	for _, r := range rules {
		b.Write(r)
	}
	return b.String()
}

// InjectCore adds the TUN inbounds and route rules of the WireGuard/AmneziaWG
// inbounds to a core config that is about to start. A failure to read the
// stored inbounds leaves the config as it was: the panel must start whatever
// happens here.
func InjectCore(inbounds []json.RawMessage, route json.RawMessage) ([]json.RawMessage, json.RawMessage, error) {
	all, err := LoadInbounds(DB())
	if err != nil {
		return inbounds, route, nil
	}
	return injectInto(all, inbounds, route)
}

func injectInto(all []Inbound, inbounds []json.RawMessage, route json.RawMessage) ([]json.RawMessage, json.RawMessage, error) {
	tuns, rules := coreFragment(all)
	coreMu.Lock()
	coreFP = fingerprint(all)
	coreMu.Unlock()
	if len(tuns) == 0 {
		return inbounds, route, nil
	}
	// An operator-made inbound with the same tag or interface name would stop
	// the core from starting; step aside rather than break the whole panel.
	taken := map[string]bool{}
	for _, raw := range inbounds {
		var probe struct {
			Tag  string `json:"tag"`
			Name string `json:"interface_name"`
		}
		if json.Unmarshal(raw, &probe) == nil {
			taken[probe.Tag], taken[probe.Name] = true, true
		}
	}
	for _, t := range tuns {
		var probe struct {
			Tag  string `json:"tag"`
			Name string `json:"interface_name"`
		}
		_ = json.Unmarshal(t, &probe)
		if taken[probe.Tag] || taken[probe.Name] {
			return inbounds, route, fmt.Errorf("wgserver: an inbound already uses tag %q or interface %q", probe.Tag, probe.Name)
		}
	}
	out := make([]json.RawMessage, 0, len(inbounds)+len(tuns))
	out = append(out, inbounds...)
	out = append(out, tuns...)

	r := map[string]json.RawMessage{}
	if len(route) > 0 && string(route) != "null" {
		if err := json.Unmarshal(route, &r); err != nil {
			return inbounds, route, fmt.Errorf("wgserver: route section: %w", err)
		}
	}
	var existing []json.RawMessage
	if raw, ok := r["rules"]; ok {
		_ = json.Unmarshal(raw, &existing)
	}
	// First, so the forced outbound beats the operator's rules, and sniffing has
	// run before any of them is evaluated.
	merged := append(append([]json.RawMessage{}, rules...), existing...)
	r["rules"], _ = json.Marshal(merged)
	newRoute, err := json.Marshal(r)
	if err != nil {
		return inbounds, route, err
	}
	return out, newRoute, nil
}

// CoreNeedsRestart reports whether the core has to be restarted: the running
// core was built with a different WireGuard/AmneziaWG fragment than the stored
// inbounds imply.
func CoreNeedsRestart() bool {
	all, err := LoadInbounds(DB())
	if err != nil {
		return false
	}
	coreMu.Lock()
	defer coreMu.Unlock()
	return coreFP != fingerprint(all)
}
