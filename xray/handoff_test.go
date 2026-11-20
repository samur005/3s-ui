package xray

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeDefaultsRouteThroughCore(t *testing.T) {
	raw, err := NormalizeVless(json.RawMessage(`{"listen_port":443,"method":"raw","security":"none"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := DecodeVless(raw)
	if !c.RouteThroughCore {
		t.Fatal("expected routeThroughCore default true")
	}
	if c.HandoffUser == "" || c.HandoffPass == "" {
		t.Fatal("expected handoff credentials")
	}
	// Explicit false sticks
	raw, err = NormalizeVless(json.RawMessage(`{"listen_port":443,"method":"raw","routeThroughCore":false}`), raw)
	if err != nil {
		t.Fatal(err)
	}
	c, _ = DecodeVless(raw)
	if c.RouteThroughCore {
		t.Fatal("expected false")
	}
	user := c.HandoffUser
	raw, err = NormalizeVless(json.RawMessage(`{"listen_port":443,"method":"raw","routeThroughCore":false}`), raw)
	if err != nil {
		t.Fatal(err)
	}
	c, _ = DecodeVless(raw)
	if c.HandoffUser != user {
		t.Fatal("handoff user must stay stable across edits")
	}
}

func TestInjectCoreAddsMixedAndSniff(t *testing.T) {
	rows := []handoffRow{{
		ID: 7, Tag: "xv1", Enable: true,
		Options: mustJSON(VlessConfig{
			ListenPort: 443, Method: "raw", Security: "none", Decryption: "none",
			RouteThroughCore: true, HandoffUser: "u", HandoffPass: "p", OutboundTag: "proxy",
		}),
	}, {
		ID: 8, Tag: "xv-off", Enable: true,
		Options: mustJSON(VlessConfig{
			ListenPort: 8443, Method: "raw", RouteThroughCore: false, HandoffUser: "u", HandoffPass: "p",
		}),
	}}
	in, route, err := injectInto(rows, []json.RawMessage{json.RawMessage(`{"type":"vless","tag":"v"}`)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(in) != 2 {
		t.Fatalf("want 2 inbounds, got %d", len(in))
	}
	var mixed map[string]any
	_ = json.Unmarshal(in[1], &mixed)
	if mixed["type"] != "mixed" || mixed["tag"] != "xv1" || mixed["listen"] != "127.0.0.1" {
		t.Fatalf("%v", mixed)
	}
	if int(mixed["listen_port"].(float64)) != HandoffPort(7) {
		t.Fatalf("port %v", mixed["listen_port"])
	}
	var r struct {
		Rules []map[string]any `json:"rules"`
	}
	_ = json.Unmarshal(route, &r)
	if len(r.Rules) < 2 {
		t.Fatalf("rules %v", r.Rules)
	}
	if r.Rules[0]["action"] != "sniff" {
		t.Fatalf("first rule should sniff: %v", r.Rules[0])
	}
	if r.Rules[1]["outbound"] != "proxy" {
		t.Fatalf("forced outbound: %v", r.Rules[1])
	}
}

func TestInjectStepsAsideForTakenTag(t *testing.T) {
	rows := []handoffRow{{
		ID: 1, Tag: "taken", Enable: true,
		Options: mustJSON(VlessConfig{ListenPort: 1, RouteThroughCore: true, HandoffUser: "u", HandoffPass: "p"}),
	}}
	_, _, err := injectInto(rows, []json.RawMessage{json.RawMessage(`{"type":"mixed","tag":"taken"}`)}, nil)
	if err == nil || !strings.Contains(err.Error(), "taken") {
		t.Fatalf("got %v", err)
	}
}
