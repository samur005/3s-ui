package xray

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func testBin(t *testing.T) string {
	t.Helper()
	if b := os.Getenv("S3UI_XRAY_BIN"); b != "" {
		return b
	}
	for _, p := range []string{"/tmp/xraybin/xray", "/tmp/xray-verified", "xray"} {
		if fileExists(p) {
			t.Setenv("S3UI_XRAY_BIN", p)
			return p
		}
	}
	t.Skip("no xray binary; set S3UI_XRAY_BIN")
	return ""
}

// freedomDirect allows loopback in tests. Production Xray 26.x freedom
// defaults block private IPs via built-in finalRules (SSRF protection).
func freedomDirect() map[string]any {
	return map[string]any{
		"protocol": "freedom",
		"tag":      "direct",
		"settings": map[string]any{
			"finalRules": []any{
				map[string]any{"action": "allow", "ip": []string{"0.0.0.0/0", "::/0"}},
			},
		},
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func startHTTPEcho(t *testing.T) (addr string, stop func()) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok-e2e-" + r.URL.Path))
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	return ln.Addr().String(), func() { srv.Close(); ln.Close() }
}

func writeJSON(t *testing.T, dir, name string, v any) string {
	t.Helper()
	p := filepath.Join(dir, name)
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func runXray(t *testing.T, bin, cfg string) (stop func()) {
	t.Helper()
	cmd := exec.Command(bin, "run", "-c", cfg)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)
	return func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
}

func socksGET(t *testing.T, socksAddr, url string) string {
	t.Helper()
	// curl --socks5-hostname keeps DNS on the proxy side
	out, err := exec.Command("curl", "-fsS", "--max-time", "8",
		"--socks5-hostname", socksAddr, url).CombinedOutput()
	if err != nil {
		t.Fatalf("curl via socks %s -> %s: %v\n%s", socksAddr, url, err, out)
	}
	return string(out)
}

func clientFromShare(t *testing.T, link string, socksPort int) map[string]any {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "vless" {
		t.Fatalf("not a vless link: %s (%v)", link, err)
	}
	uuid := u.User.Username()
	hostport := u.Host
	q := map[string]string{}
	for k, vs := range u.Query() {
		if len(vs) > 0 {
			q[k] = vs[0]
		}
	}
	typ := q["type"]
	if typ == "tcp" {
		typ = "raw"
	}
	if typ == "ws" {
		typ = "websocket"
	}
	stream := map[string]any{
		"method":   typ,
		"security": q["security"],
	}
	switch typ {
	case "xhttp":
		xs := map[string]any{"path": q["path"], "mode": "auto"}
		if q["mode"] != "" {
			xs["mode"] = q["mode"]
		}
		if q["host"] != "" {
			xs["host"] = q["host"]
		}
		stream["xhttpSettings"] = xs
	case "websocket":
		stream["wsSettings"] = map[string]any{"path": q["path"], "host": q["host"]}
	case "httpupgrade":
		stream["httpupgradeSettings"] = map[string]any{"path": q["path"], "host": q["host"]}
	case "grpc":
		stream["grpcSettings"] = map[string]any{"serviceName": q["serviceName"]}
	case "raw":
		stream["rawSettings"] = map[string]any{}
	}
	if q["security"] == "reality" {
		stream["realitySettings"] = map[string]any{
			"serverName":  q["sni"],
			"publicKey":   q["pbk"],
			"shortId":     q["sid"],
			"fingerprint": q["fp"],
			"spiderX":     q["spx"],
		}
	}
	user := map[string]any{"id": uuid, "encryption": "none"}
	if e := q["encryption"]; e != "" {
		user["encryption"] = e
	}
	if f := q["flow"]; f != "" {
		user["flow"] = f
	}
	return map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"inbounds": []any{map[string]any{
			"listen":   "127.0.0.1",
			"port":     socksPort,
			"protocol": "socks",
			"settings": map[string]any{"udp": false},
		}},
		"outbounds": []any{map[string]any{
			"protocol": "vless",
			"settings": map[string]any{
				"vnext": []any{map[string]any{
					"address": hostOf(hostport),
					"port":    portOf(hostport),
					"users":   []any{user},
				}},
			},
			"streamSettings": stream,
		}},
	}
}

func mustAtoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func TestE2E_PanelShareLink_RawAndXHTTP(t *testing.T) {
	bin := testBin(t)
	dir := t.TempDir()
	httpAddr, stopHTTP := startHTTPEcho(t)
	defer stopHTTP()
	uuid := "5783a3e7-e373-51cd-8642-c83782b807c5"

	cases := []struct {
		name string
		cfg  VlessConfig
	}{
		{
			name: "raw-none",
			cfg: VlessConfig{
				Listen: "127.0.0.1", ListenPort: freePort(t), Endpoint: "127.0.0.1",
				Method: "raw", Security: "none", Decryption: "none",
			},
		},
		{
			name: "xhttp-none",
			cfg: VlessConfig{
				Listen: "127.0.0.1", ListenPort: freePort(t), Endpoint: "127.0.0.1",
				Method: "xhttp", Security: "none", Decryption: "none",
				Path: "/api", XHTTPMode: "auto",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := NormalizeVless(mustJSON(tc.cfg), nil)
			if err != nil {
				t.Fatal(err)
			}
			cfg, _ := DecodeVless(raw)
			ib, err := buildInbound("e2e-"+tc.name, cfg, []ClientCred{{Email: "u", ID: uuid, Enable: true}})
			if err != nil {
				t.Fatal(err)
			}
			serverDoc := map[string]any{
				"log":       map[string]any{"loglevel": "warning"},
				"inbounds":  []any{ib},
				"outbounds": []any{freedomDirect()},
			}
			srvPath := writeJSON(t, dir, tc.name+"-srv.json", serverDoc)
			// Official schema check with pinned binary before run.
			if out, err := exec.Command(bin, "run", "-test", "-c", srvPath).CombinedOutput(); err != nil {
				t.Fatalf("server -test: %v\n%s\n%s", err, out, mustRaw(serverDoc))
			}
			stopSrv := runXray(t, bin, srvPath)
			defer stopSrv()

			link, err := ShareLink(cfg, uuid, "e2e")
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(link, "privateKey") || strings.Contains(link, cfg.RealityPrivateKey) && cfg.RealityPrivateKey != "" {
				t.Fatalf("share link leaked private key: %s", link)
			}
			socksPort := freePort(t)
			cliDoc := clientFromShare(t, link, socksPort)
			cliPath := writeJSON(t, dir, tc.name+"-cli.json", cliDoc)
			if out, err := exec.Command(bin, "run", "-test", "-c", cliPath).CombinedOutput(); err != nil {
				t.Fatalf("client -test: %v\n%s\nlink=%s\n%s", err, out, link, mustRaw(cliDoc))
			}
			stopCli := runXray(t, bin, cliPath)
			defer stopCli()

			body := socksGET(t, fmt.Sprintf("127.0.0.1:%d", socksPort), "http://"+httpAddr+"/via-"+tc.name)
			if !strings.Contains(body, "ok-e2e") {
				t.Fatalf("unexpected body: %q", body)
			}
			t.Logf("OK %s via share link HTTP body=%q", tc.name, body)
		})
	}
}

func TestE2E_VLESSEncryption_MLKEM(t *testing.T) {
	bin := testBin(t)
	dec, enc, err := GenerateVlessEnc(bin)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dec, "mlkem") || !strings.Contains(enc, "mlkem") {
		t.Fatalf("unexpected enc pair: %s / %s", dec, enc)
	}
	dir := t.TempDir()
	httpAddr, stopHTTP := startHTTPEcho(t)
	defer stopHTTP()
	uuid := "5783a3e7-e373-51cd-8642-c83782b807c5"
	port := freePort(t)
	cfg := VlessConfig{
		Listen: "127.0.0.1", ListenPort: port, Endpoint: "127.0.0.1",
		Method: "raw", Security: "none", Decryption: dec, Encryption: enc,
	}
	raw, err := NormalizeVless(mustJSON(cfg), nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ = DecodeVless(raw)
	ib, err := buildInbound("e2e-enc", cfg, []ClientCred{{Email: "u", ID: uuid, Enable: true}})
	if err != nil {
		t.Fatal(err)
	}
	srvPath := writeJSON(t, dir, "enc-srv.json", map[string]any{
		"log":       map[string]any{"loglevel": "warning"},
		"inbounds":  []any{ib},
		"outbounds": []any{freedomDirect()},
	})
	if out, err := exec.Command(bin, "run", "-test", "-c", srvPath).CombinedOutput(); err != nil {
		t.Fatalf("enc server -test: %v\n%s", err, out)
	}
	stopSrv := runXray(t, bin, srvPath)
	defer stopSrv()

	link, err := ShareLink(cfg, uuid, "enc")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(link, "encryption=") || strings.Contains(link, "encryption=none") {
		t.Fatalf("share link missing encryption: %s", link)
	}
	socksPort := freePort(t)
	cliPath := writeJSON(t, dir, "enc-cli.json", clientFromShare(t, link, socksPort))
	stopCli := runXray(t, bin, cliPath)
	defer stopCli()
	body := socksGET(t, fmt.Sprintf("127.0.0.1:%d", socksPort), "http://"+httpAddr+"/enc")
	if !strings.Contains(body, "ok-e2e") {
		t.Fatalf("enc body: %q", body)
	}
	t.Logf("OK encryption ML-KEM HTTP body=%q", body)
}

func TestE2E_Reality_IfReachable(t *testing.T) {
	bin := testBin(t)
	// Need outbound TCP 443 to a REALITY dest.
	conn, err := net.DialTimeout("tcp", "www.microsoft.com:443", 3*time.Second)
	if err != nil {
		t.Skip("no outbound to www.microsoft.com:443:", err)
	}
	conn.Close()

	out, err := exec.Command(bin, "x25519").CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	priv, pub := "", ""
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "Private") {
			priv = strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
		}
		if strings.Contains(line, "Password") || strings.Contains(line, "Public") {
			pub = strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
		}
	}
	if priv == "" || pub == "" {
		t.Fatalf("x25519 parse failed: %s", out)
	}

	dir := t.TempDir()
	httpAddr, stopHTTP := startHTTPEcho(t)
	defer stopHTTP()
	uuid := "5783a3e7-e373-51cd-8642-c83782b807c5"
	port := freePort(t)
	cfg := VlessConfig{
		Listen: "127.0.0.1", ListenPort: port, Endpoint: "127.0.0.1",
		Method: "raw", Security: "reality", Decryption: "none",
		RealityDest:        "www.microsoft.com:443",
		RealityServerNames: []string{"www.microsoft.com"},
		RealityPrivateKey:  priv, RealityPublicKey: pub,
		RealityShortIds: []string{""}, RealityFingerprint: "chrome",
	}
	raw, err := NormalizeVless(mustJSON(cfg), nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ = DecodeVless(raw)
	ib, err := buildInbound("e2e-reality", cfg, []ClientCred{{Email: "u", ID: uuid, Enable: true}})
	if err != nil {
		t.Fatal(err)
	}
	// Confirm panel emits "method" + realitySettings.target
	ss := ib["streamSettings"].(map[string]any)
	if ss["method"] != "raw" {
		t.Fatalf("expected method=raw, got %v", ss["method"])
	}
	rs := ss["realitySettings"].(map[string]any)
	if _, ok := rs["target"]; !ok {
		t.Fatalf("panel should emit realitySettings.target: %#v", rs)
	}
	if _, ok := rs["privateKey"]; !ok {
		t.Fatal("server config missing privateKey")
	}

	srvPath := writeJSON(t, dir, "reality-srv.json", map[string]any{
		"log":       map[string]any{"loglevel": "warning"},
		"inbounds":  []any{ib},
		"outbounds": []any{freedomDirect()},
	})
	if out, err := exec.Command(bin, "run", "-test", "-c", srvPath).CombinedOutput(); err != nil {
		t.Fatalf("reality -test: %v\n%s", err, out)
	}
	stopSrv := runXray(t, bin, srvPath)
	defer stopSrv()

	link, err := ShareLink(cfg, uuid, "reality")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(link, priv) {
		t.Fatal("share link leaked Reality private key")
	}
	if !strings.Contains(link, "security=reality") || !strings.Contains(link, "pbk=") {
		t.Fatalf("bad reality link: %s", link)
	}
	socksPort := freePort(t)
	cliPath := writeJSON(t, dir, "reality-cli.json", clientFromShare(t, link, socksPort))
	stopCli := runXray(t, bin, cliPath)
	defer stopCli()
	body := socksGET(t, fmt.Sprintf("127.0.0.1:%d", socksPort), "http://"+httpAddr+"/reality")
	if !strings.Contains(body, "ok-e2e") {
		t.Fatalf("reality body: %q", body)
	}
	t.Logf("OK Reality HTTP body=%q", body)
}

func TestE2E_ManagerLifecycle(t *testing.T) {
	bin := testBin(t)
	dir := t.TempDir()
	t.Setenv("S3UI_XRAY_DIR", dir)
	port := freePort(t)
	uuid := "5783a3e7-e373-51cd-8642-c83782b807c5"
	cfg := VlessConfig{
		Listen: "127.0.0.1", ListenPort: port, Endpoint: "127.0.0.1",
		Method: "raw", Security: "none", Decryption: "none",
	}
	ib, err := buildInbound("life", cfg, []ClientCred{{Email: "u", ID: uuid, Enable: true}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{
		"log":       map[string]any{"loglevel": "warning"},
		"inbounds":  []any{ib},
		"outbounds": []any{freedomDirect()},
		"routing":   map[string]any{"domainStrategy": "AsIs", "rules": []any{}},
	})
	m := &Manager{}
	m.Apply(func() ([]byte, error) { return body, nil })
	time.Sleep(500 * time.Millisecond)
	st := m.Status(1)
	if !st.Running || !st.BinaryOK {
		t.Fatalf("expected running: %+v", st)
	}
	if st.Version == "" || !strings.Contains(st.Pinned, "26.11.9") {
		t.Fatalf("version/pinned: %+v", st)
	}
	cfgPath := filepath.Join(dir, "config.json")
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"method":"raw"`) && !strings.Contains(string(raw), `"method": "raw"`) {
		t.Fatalf("config missing method: %s", raw)
	}
	// Hot rebuild: change port and Apply again
	port2 := freePort(t)
	cfg.ListenPort = port2
	ib2, _ := buildInbound("life", cfg, []ClientCred{{Email: "u", ID: uuid, Enable: true}})
	body2, _ := json.Marshal(map[string]any{
		"log":       map[string]any{"loglevel": "warning"},
		"inbounds":  []any{ib2},
		"outbounds": []any{freedomDirect()},
		"routing":   map[string]any{"rules": []any{}},
	})
	m.Apply(func() ([]byte, error) { return body2, nil })
	time.Sleep(500 * time.Millisecond)
	if !m.Status(1).Running {
		t.Fatal("not running after hot Apply")
	}
	// Empty inbounds stops process
	empty, _ := json.Marshal(map[string]any{
		"inbounds": []any{}, "outbounds": []any{freedomDirect()},
	})
	m.Apply(func() ([]byte, error) { return empty, nil })
	time.Sleep(300 * time.Millisecond)
	if m.Status(0).Running {
		t.Fatal("should have stopped with 0 inbounds")
	}
	m.Shutdown()
	_ = bin
}

func TestSchema_MethodPreferredOverNetwork(t *testing.T) {
	bin := testBin(t)
	dir := t.TempDir()
	uuid := "5783a3e7-e373-51cd-8642-c83782b807c5"
	cfg := VlessConfig{Listen: "127.0.0.1", ListenPort: freePort(t), Method: "xhttp", Security: "none", Path: "/p", XHTTPMode: "stream-up", Decryption: "none"}
	ib, err := buildInbound("schema", cfg, []ClientCred{{Email: "a", ID: uuid, Enable: true}})
	if err != nil {
		t.Fatal(err)
	}
	ss := ib["streamSettings"].(map[string]any)
	if _, hasNet := ss["network"]; hasNet {
		t.Fatal("panel must emit method, not legacy network")
	}
	if ss["method"] != "xhttp" {
		t.Fatalf("method=%v", ss["method"])
	}
	xs := ss["xhttpSettings"].(map[string]any)
	if xs["mode"] != "stream-up" {
		t.Fatalf("xhttp mode=%v", xs["mode"])
	}
	doc := map[string]any{"inbounds": []any{ib}, "outbounds": []any{map[string]any{"protocol": "freedom"}}}
	p := writeJSON(t, dir, "schema.json", doc)
	out, err := exec.Command(bin, "run", "-test", "-c", p).CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestShareLink_NoCredentialLeak(t *testing.T) {
	cfg := VlessConfig{
		ListenPort: 443, Endpoint: "vpn.example.com", Method: "raw", Security: "reality",
		RealityPrivateKey: "SECRET_PRIVATE_KEY_DO_NOT_LEAK",
		RealityPublicKey:  "pub", RealityServerNames: []string{"www.example.com"},
		RealityShortIds: []string{"abcd"}, RealityFingerprint: "chrome",
		Decryption: "mlkem-SECRET-DEC", Encryption: "mlkem-CLIENT-ENC",
	}
	link, err := ShareLink(cfg, "5783a3e7-e373-51cd-8642-c83782b807c5", "t")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"SECRET_PRIVATE", "mlkem-SECRET-DEC", "privateKey"} {
		if strings.Contains(link, bad) {
			t.Fatalf("leaked %q in %s", bad, link)
		}
	}
	if !strings.Contains(link, "mlkem-CLIENT-ENC") {
		t.Fatal("client encryption should be in share link")
	}
}

func hostOf(hostport string) string {
	h, _, err := net.SplitHostPort(hostport)
	if err != nil {
		return hostport
	}
	return h
}

func portOf(hostport string) int {
	_, p, err := net.SplitHostPort(hostport)
	if err != nil {
		return 0
	}
	return mustAtoi(p)
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func mustRaw(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}
