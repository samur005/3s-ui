package xray

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeVlessDefaultsAndRefuses(t *testing.T) {
	raw, err := NormalizeVless(0, json.RawMessage(`{"listen_port":443,"method":"xhttp","security":"reality"}`), nil)
	if err == nil {
		t.Fatal("reality without keys should fail")
	}
	raw, err = NormalizeVless(0, json.RawMessage(`{
		"listen_port":443,"method":"xhttp","security":"reality",
		"realityDest":"www.cloudflare.com:443","realityPrivateKey":"abc",
		"realityServerNames":["www.cloudflare.com"],"path":"/api"
	}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := DecodeVless(raw)
	if cfg.XHTTPMode != "auto" || cfg.Method != "xhttp" || cfg.Decryption != "none" {
		t.Fatalf("%+v", cfg)
	}
	_, err = NormalizeVless(0, json.RawMessage(`{"listen_port":443,"method":"mkcp","security":"reality"}`), nil)
	if err == nil || !strings.Contains(err.Error(), "REALITY") {
		t.Fatalf("mkcp+reality: %v", err)
	}
	_, err = NormalizeVless(0, json.RawMessage(`{"listen_port":443,"method":"ws","flow":"xtls-rprx-vision"}`), nil)
	if err == nil {
		t.Fatal("vision on ws should fail")
	}
	_, err = NormalizeVless(0, json.RawMessage(`{"listen_port":443,"method":"tcp","security":"tls","certFile":"/c","keyFile":"/k","flow":"xtls-rprx-vision"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestShareLink(t *testing.T) {
	cfg := VlessConfig{
		ListenPort: 443, Endpoint: "vpn.example.com", Method: "xhttp", Security: "reality",
		Path: "/v", XHTTPMode: "auto", RealityPublicKey: "pbk", RealityServerNames: []string{"www.cloudflare.com"},
		RealityShortIds: []string{"abcd"}, RealityFingerprint: "chrome",
	}
	link, err := ShareLink(cfg, "5783a3e7-e373-51cd-8642-c83782b807c5", "t")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(link, "vless://") || !strings.Contains(link, "type=xhttp") || !strings.Contains(link, "security=reality") {
		t.Fatal(link)
	}
}

func TestBuildConfigPassesXrayTest(t *testing.T) {
	bin := os.Getenv("S3UI_XRAY_BIN")
	if bin == "" {
		if p := "/tmp/xraybin/xray"; fileExists(p) {
			bin = p
			t.Setenv("S3UI_XRAY_BIN", bin)
		}
	}
	if bin == "" {
		t.Skip("no xray binary")
	}
	cfg := VlessConfig{
		Listen: "127.0.0.1", ListenPort: 10443, Method: "raw", Security: "none", Decryption: "none",
	}
	raw, _ := cfg.Encode()
	ib, err := buildInbound("xv1", cfg, []ClientCred{{Email: "a", ID: "5783a3e7-e373-51cd-8642-c83782b807c5", Enable: true}})
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{
		"log":       map[string]any{"loglevel": "warning"},
		"inbounds":  []any{ib},
		"outbounds": []any{map[string]any{"protocol": "freedom", "tag": "direct"}},
	}
	body, _ := json.MarshalIndent(doc, "", "  ")
	dir := t.TempDir()
	path := filepath.Join(dir, "c.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bin, "run", "-test", "-c", path).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s\n%s", err, out, body)
	}
	_ = raw
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func TestParseVlessEnc(t *testing.T) {
	sample := `Authentication: ML-KEM-768, Post-Quantum
"decryption": "mlkem768x25519plus.native.600s.AAA"
"encryption": "mlkem768x25519plus.native.0rtt.BBB"
`
	d, e, err := ParseVlessEncOutput(sample)
	if err != nil || d == "" || e == "" {
		t.Fatal(d, e, err)
	}
}
