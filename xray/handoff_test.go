package xray

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeDefaultsRouteThroughCore(t *testing.T) {
	raw, err := NormalizeVless(1, json.RawMessage(`{"listen_port":443,"method":"raw","security":"none"}`), nil)
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
	if c.HandoffPort < handoffPortBase || c.HandoffPort >= handoffPortBase+handoffPortSpan {
		t.Fatalf("handoff port %d", c.HandoffPort)
	}
	port := c.HandoffPort
	raw, err = NormalizeVless(1, json.RawMessage(`{"listen_port":443,"method":"raw","routeThroughCore":false}`), raw)
	if err != nil {
		t.Fatal(err)
	}
	c, _ = DecodeVless(raw)
	if c.RouteThroughCore {
		t.Fatal("expected false")
	}
	user := c.HandoffUser
	raw, err = NormalizeVless(1, json.RawMessage(`{"listen_port":443,"method":"raw","routeThroughCore":true}`), raw)
	if err != nil {
		t.Fatal(err)
	}
	c, _ = DecodeVless(raw)
	if c.HandoffUser != user || c.HandoffPort != port {
		t.Fatalf("handoff must stay stable: user %s→%s port %d→%d", user, c.HandoffUser, port, c.HandoffPort)
	}
}

func TestInjectCoreAddsMixedAndSniff(t *testing.T) {
	rows := []handoffRow{{
		ID: 7, Tag: "xv1", Enable: true,
		Options: mustJSON(VlessConfig{
			ListenPort: 443, Method: "raw", Security: "none", Decryption: "none",
			RouteThroughCore: true, HandoffUser: "u", HandoffPass: "p", HandoffPort: 41207, OutboundTag: "proxy",
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
	if int(mixed["listen_port"].(float64)) != 41207 {
		t.Fatalf("port %v", mixed["listen_port"])
	}
	users := mixed["users"].([]any)
	u0 := users[0].(map[string]any)
	if u0["username"] != "u" || u0["password"] != "p" {
		t.Fatalf("users %v", users)
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
		Options: mustJSON(VlessConfig{ListenPort: 1, RouteThroughCore: true, HandoffUser: "u", HandoffPass: "p", HandoffPort: 41201}),
	}}
	_, _, err := injectInto(rows, []json.RawMessage{json.RawMessage(`{"type":"mixed","tag":"taken"}`)}, nil)
	if err == nil || !strings.Contains(err.Error(), "taken") {
		t.Fatalf("got %v", err)
	}
}

func TestInjectRejectsMissingCreds(t *testing.T) {
	rows := []handoffRow{{
		ID: 1, Tag: "xv", Enable: true,
		Options: mustJSON(VlessConfig{ListenPort: 1, RouteThroughCore: true, HandoffPort: 41201}),
	}}
	_, _, err := injectInto(rows, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "credentials") {
		t.Fatalf("got %v", err)
	}
}

func TestInjectRejectsPortCollision(t *testing.T) {
	rows := []handoffRow{
		{ID: 1, Tag: "a", Enable: true, Options: mustJSON(VlessConfig{RouteThroughCore: true, HandoffUser: "u", HandoffPass: "p", HandoffPort: 41250})},
		{ID: 2, Tag: "b", Enable: true, Options: mustJSON(VlessConfig{RouteThroughCore: true, HandoffUser: "u", HandoffPass: "p", HandoffPort: 41250})},
	}
	_, _, err := injectInto(rows, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "41250") {
		t.Fatalf("got %v", err)
	}
}
