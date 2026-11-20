package bisync

import "testing"

func TestInboundTemplateStripsListenAndServer(t *testing.T) {
	p := InboundTemplatePayload(map[string]any{
		"type": "vless", "tag": "v1", "enable": true,
		"options": map[string]any{"listen": "0.0.0.0", "listen_port": 443, "flow": "xtls-rprx-vision"},
		"out_json": map[string]any{"server": "1.2.3.4", "server_port": 443, "flow": "xtls-rprx-vision"},
	})
	opts := p["options"].(map[string]any)
	if _, ok := opts["listen"]; ok {
		t.Fatal("listen leaked")
	}
	if opts["flow"] != "xtls-rprx-vision" {
		t.Fatal("flow missing")
	}
	oj := p["out_json"].(map[string]any)
	if _, ok := oj["server"]; ok {
		t.Fatal("server leaked")
	}
}

func TestUnsupportedInboundClone(t *testing.T) {
	if !UnsupportedInboundClone("qwdtt") || UnsupportedInboundClone("vless") {
		t.Fatal("matrix")
	}
}
