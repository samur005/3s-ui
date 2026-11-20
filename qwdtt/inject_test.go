package qwdtt

import (
	"encoding/json"
	"testing"
)

func raws(t *testing.T, objs ...string) []json.RawMessage {
	t.Helper()
	var out []json.RawMessage
	for _, o := range objs {
		out = append(out, json.RawMessage(o))
	}
	return out
}

func onCfg() Config {
	c := Default()
	c.Enabled = true
	return c
}

func TestInjectIsNoopWhenOffOrNotRouted(t *testing.T) {
	in := raws(t, `{"type":"vless","tag":"a"}`)
	route := json.RawMessage(`{"rules":[{"outbound":"direct"}]}`)

	off := Default() // disabled
	gotIn, gotRoute, err := injectInto(off, in, route)
	if err != nil || len(gotIn) != 1 || string(gotRoute) != string(route) {
		t.Errorf("disabled changed the config: %v %s %v", gotIn, gotRoute, err)
	}
	direct := onCfg()
	direct.RouteThroughCore = false
	gotIn, gotRoute, _ = injectInto(direct, in, route)
	if len(gotIn) != 1 || string(gotRoute) != string(route) {
		t.Error("RouteThroughCore=false changed the config")
	}
}

func TestInjectAddsTunAndRulesFirst(t *testing.T) {
	if !RoutingSupported() {
		t.Skip("Linux only")
	}
	in := raws(t, `{"type":"vless","tag":"a"}`)
	route := json.RawMessage(`{"final":"direct","rules":[{"domain":["x.com"],"outbound":"block"}]}`)
	c := onCfg()
	c.OutboundTag = "proxy-out"
	gotIn, gotRoute, err := injectInto(c, in, route)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotIn) != 2 {
		t.Fatalf("inbounds = %d", len(gotIn))
	}
	var tun map[string]any
	_ = json.Unmarshal(gotIn[1], &tun)
	if tun["type"] != "tun" || tun["tag"] != TunTag || tun["interface_name"] != TunName ||
		tun["auto_route"] != false || tun["stack"] != "system" {
		t.Errorf("tun = %v", tun)
	}
	if addr, _ := tun["address"].([]any); len(addr) != 1 || addr[0] != TunAddress {
		t.Errorf("tun address = %v", tun["address"])
	}
	var r struct {
		Final string           `json:"final"`
		Rules []map[string]any `json:"rules"`
	}
	if err := json.Unmarshal(gotRoute, &r); err != nil {
		t.Fatal(err)
	}
	if r.Final != "direct" {
		t.Error("an unrelated route key was lost")
	}
	if len(r.Rules) != 3 {
		t.Fatalf("rules = %v", r.Rules)
	}
	if r.Rules[0]["action"] != "sniff" || r.Rules[1]["action"] != "route" || r.Rules[1]["outbound"] != "proxy-out" {
		t.Errorf("panel rules not first / wrong: %v", r.Rules[:2])
	}
	if r.Rules[2]["outbound"] != "block" {
		t.Errorf("operator rule lost or reordered: %v", r.Rules[2])
	}
	// The input must not be modified in place.
	if len(in) != 1 {
		t.Error("input slice was changed")
	}
}

func TestInjectWithoutRouteSectionOrForcedOutbound(t *testing.T) {
	if !RoutingSupported() {
		t.Skip("Linux only")
	}
	for _, route := range []json.RawMessage{nil, json.RawMessage("null"), json.RawMessage("{}")} {
		_, gotRoute, err := injectInto(onCfg(), nil, route)
		if err != nil {
			t.Fatalf("route %q: %v", route, err)
		}
		var r struct {
			Rules []map[string]any `json:"rules"`
		}
		_ = json.Unmarshal(gotRoute, &r)
		if len(r.Rules) != 1 || r.Rules[0]["action"] != "sniff" {
			t.Errorf("route %q -> %s", route, gotRoute)
		}
	}
}

func TestInjectStepsAsideForAnOperatorInbound(t *testing.T) {
	if !RoutingSupported() {
		t.Skip("Linux only")
	}
	in := raws(t, `{"type":"tun","tag":"mine","interface_name":"`+TunName+`"}`)
	gotIn, _, err := injectInto(onCfg(), in, nil)
	if err == nil || len(gotIn) != 1 {
		t.Errorf("a clash on the interface name must be reported and nothing added: %v %v", err, gotIn)
	}
}

func TestCoreNeedsRestartTracksTheFragment(t *testing.T) {
	if !RoutingSupported() {
		t.Skip("Linux only")
	}
	off := Default()
	_, _, _ = injectInto(off, nil, nil)
	if CoreNeedsRestart(off) {
		t.Error("core built without qWDTT, config without: no restart needed")
	}
	on := onCfg()
	if !CoreNeedsRestart(on) {
		t.Error("enabling must need a restart")
	}
	_, _, _ = injectInto(on, nil, nil)
	if CoreNeedsRestart(on) {
		t.Error("core already carries the fragment")
	}
	forced := on
	forced.OutboundTag = "x"
	if !CoreNeedsRestart(forced) {
		t.Error("a forced outbound changes the rules and must need a restart")
	}
	changedPort := on
	changedPort.ListenAddr = "0.0.0.0:443"
	if CoreNeedsRestart(changedPort) {
		t.Error("the DTLS port does not touch the core config")
	}
	if !CoreNeedsRestart(off) {
		t.Error("disabling must need a restart")
	}
}
