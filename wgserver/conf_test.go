package wgserver

import (
	"net/url"
	"strings"
	"testing"
)

func testCfg(t *testing.T, typ string) Config {
	t.Helper()
	priv, pub, err := NewKeyPair()
	if err != nil || priv == "" {
		t.Fatal(err)
	}
	cfg := defaults(typ)
	cfg.ListenPort = 51820
	cfg.PrivateKey, cfg.PublicKey = priv, pub
	cfg.Subnet = "10.8.1.0/24"
	if typ == TypeAWG {
		cfg.Version = "3.1"
		cfg.Obfuscation = Generate("3.1")
	}
	return cfg
}

func testPeer(t *testing.T) Peer {
	t.Helper()
	priv, pub, _ := NewKeyPair()
	psk, _ := NewPresharedKey()
	return Peer{ClientID: 1, ClientName: "alice", ClientEnable: true, Bound: true, PrivateKey: priv, PublicKey: pub, PresharedKey: psk, Address: "10.8.1.2"}
}

func TestKeyPairs(t *testing.T) {
	priv, pub, err := NewKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := PublicKey(priv); err != nil || got != pub {
		t.Errorf("derived %q (%v), want %q", got, err, pub)
	}
	if ValidKey("nope") || !ValidKey(priv) {
		t.Error("ValidKey")
	}
}

func TestPlainWireGuardClient(t *testing.T) {
	cfg := testCfg(t, TypeWG)
	p := testPeer(t)
	cc, err := BuildClient(TypeWG, cfg, p, "vpn.example.com", "alice · wg")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"[Interface]", "PrivateKey = " + p.PrivateKey, "Address = 10.8.1.2/32", "DNS = 1.1.1.1, 8.8.8.8", "MTU = 1420",
		"[Peer]", "PublicKey = " + cfg.PublicKey, "PresharedKey = " + p.PresharedKey,
		"AllowedIPs = 0.0.0.0/0, ::/0", "Endpoint = vpn.example.com:51820", "PersistentKeepalive = 25",
	} {
		if !strings.Contains(cc.Conf, want) {
			t.Errorf("conf lacks %q:\n%s", want, cc.Conf)
		}
	}
	if strings.Contains(cc.Conf, "Jc =") || cc.Vpn != "" {
		t.Error("a plain WireGuard config carries no AmneziaWG fields")
	}
	u, err := url.Parse(cc.URI)
	if err != nil || u.Scheme != "wireguard" {
		t.Fatalf("uri %q: %v", cc.URI, err)
	}
	if pw := u.User.Username(); pw != p.PrivateKey {
		t.Errorf("uri key = %q", pw)
	}
	q := u.Query()
	if u.Host != "vpn.example.com:51820" || q.Get("publickey") != cfg.PublicKey || q.Get("address") != "10.8.1.2/32" || q.Get("presharedkey") != p.PresharedKey {
		t.Errorf("uri = %s", cc.URI)
	}
	if u.Fragment != "alice · wg" {
		t.Errorf("fragment = %q", u.Fragment)
	}
	if cc.Filename != "alice__wg.conf" && !strings.HasSuffix(cc.Filename, ".conf") {
		t.Errorf("filename = %q", cc.Filename)
	}
}

func TestEndpointOverrides(t *testing.T) {
	cfg := testCfg(t, TypeWG)
	p := testPeer(t)
	cfg.Endpoint = "edge.example.com:443"
	cc, _ := BuildClient(TypeWG, cfg, p, "ignored", "x")
	if !strings.Contains(cc.Conf, "Endpoint = edge.example.com:443") {
		t.Errorf("explicit endpoint with port must win:\n%s", cc.Conf)
	}
	cfg.Endpoint = "2001:db8::1"
	cc, _ = BuildClient(TypeWG, cfg, p, "", "x")
	if !strings.Contains(cc.Conf, "Endpoint = [2001:db8::1]:51820") {
		t.Errorf("IPv6 host must be bracketed:\n%s", cc.Conf)
	}
	cfg.Endpoint = ""
	if _, err := BuildClient(TypeWG, cfg, p, "", "x"); err == nil {
		t.Error("no endpoint anywhere must be an error")
	}
}

func TestAmneziaClientCarriesTheVersionsFields(t *testing.T) {
	cases := map[string]struct {
		want, not []string
		proto     string
	}{
		"1.0": {[]string{"Jc =", "S1 =", "S2 =", "H1 ="}, []string{"S3 =", "I1 =", "HeaderProtectionKey"}, ""},
		"1.5": {[]string{"Jc =", "I1 ="}, []string{"S3 =", "HeaderProtectionKey"}, "2"},
		"2.0": {[]string{"S3 =", "S4 =", "I1 ="}, []string{"HeaderProtectionKey", "RandomTrailers"}, "2"},
		"2.5": {[]string{"S3 =", "S4 =", "I1 ="}, []string{"HeaderProtectionKey", "RandomTrailers"}, "2"},
		"3.0": {[]string{"HeaderProtectionKey =", "ContentPaddingAddition =", "RekeyAfterTime ="}, []string{"RandomTrailers"}, "3"},
		"3.1": {[]string{"HeaderProtectionKey =", "RandomTrailers = true", "DisableCookies = true"}, nil, "3"},
	}
	for v, c := range cases {
		cfg := testCfg(t, TypeAWG)
		cfg.Version = v
		cfg.Obfuscation = Generate(v)
		cc, err := BuildClient(TypeAWG, cfg, testPeer(t), "vpn.example.com", "alice")
		if err != nil {
			t.Fatalf("%s: %v", v, err)
		}
		for _, w := range c.want {
			if !strings.Contains(cc.Conf, w) {
				t.Errorf("%s: conf lacks %q:\n%s", v, w, cc.Conf)
			}
		}
		for _, n := range c.not {
			if strings.Contains(cc.Conf, n) {
				t.Errorf("%s: conf must not have %q", v, n)
			}
		}
		if cc.URI != "" || !strings.HasPrefix(cc.Vpn, "vpn://") {
			t.Errorf("%s: links = %q %q", v, cc.URI, cc.Vpn)
		}
		env, err := DecodeVpnURI(cc.Vpn)
		if err != nil {
			t.Fatalf("%s: vpn:// does not decode: %v", v, err)
		}
		cont := env["containers"].([]any)[0].(map[string]any)["awg"].(map[string]any)
		if pv, _ := cont["protocol_version"].(string); pv != c.proto {
			t.Errorf("%s: protocol_version = %q, want %q", v, pv, c.proto)
		}
		if env["hostName"] != "vpn.example.com" {
			t.Errorf("%s: hostName = %v", v, env["hostName"])
		}
		if !strings.Contains(cont["last_config"].(string), "client_priv_key") {
			t.Errorf("%s: last_config lacks the structured keys", v)
		}
	}
}

func TestConfValuesCannotBreakOutOfTheirLine(t *testing.T) {
	cfg := testCfg(t, TypeAWG)
	cc, err := BuildClient(TypeAWG, cfg, testPeer(t), "h.example", "evil\n[Interface]\nPostUp = rm -rf /")
	if err != nil {
		t.Fatal(err)
	}
	sections, postUp := 0, 0
	for _, line := range strings.Split(cc.Conf, "\n") {
		if strings.HasPrefix(line, "[Interface]") {
			sections++
		}
		if strings.HasPrefix(line, "PostUp") {
			postUp++
		}
	}
	if sections != 1 || postUp != 0 {
		t.Errorf("injected a section or key:\n%s", cc.Conf)
	}
}

func TestDeviceConfigPerVersion(t *testing.T) {
	cfg := testCfg(t, TypeWG)
	text, err := deviceConfig(TypeWG, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "listen_port=51820") || strings.Contains(text, "jc=") {
		t.Errorf("plain WireGuard config:\n%s", text)
	}
	for _, v := range AWGVersions {
		c := testCfg(t, TypeAWG)
		c.Version = v
		c.Obfuscation = Generate(v)
		text, err := deviceConfig(TypeAWG, c)
		if err != nil {
			t.Fatalf("%s: %v", v, err)
		}
		has := func(s string) bool { return strings.Contains(text, s) }
		f := Fields(v)
		if !has("jc=") || !has("s1=") || !has("h1=") {
			t.Errorf("%s: base lines missing", v)
		}
		if has("s3=") != f.S34 || has("i1=") != f.CPS || has("header_protection_key=") != f.Protection || has("random_trailers=") != f.TrailersCookie {
			t.Errorf("%s: wrong lines:\n%s", v, text)
		}
	}
	bad := testCfg(t, TypeAWG)
	bad.Version = "3.1"
	bad.Obfuscation = Generate("3.1")
	bad.I1 = "<c>"
	if _, err := deviceConfig(TypeAWG, bad); err == nil {
		t.Error("a descriptor that aborts the engine must not reach it")
	}
}

func TestPeerLines(t *testing.T) {
	p := testPeer(t)
	add, err := peerAdd(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(add, "allowed_ip=10.8.1.2/32") || !strings.Contains(add, "replace_allowed_ips=true") || !strings.Contains(add, "preshared_key=") {
		t.Errorf("add:\n%s", add)
	}
	rm, _ := peerRemove(p.PublicKey)
	if !strings.Contains(rm, "remove=true") {
		t.Errorf("remove: %s", rm)
	}
	if _, err := peerAdd(Peer{PublicKey: "x"}); err == nil {
		t.Error("bad key accepted")
	}
}

func TestParseStats(t *testing.T) {
	_, pub, _ := NewKeyPair()
	hexKey, _ := keyHex(pub)
	got := parseStats("private_key=aa\nlisten_port=1\npublic_key=" + hexKey + "\nlast_handshake_time_sec=1700000000\ntx_bytes=10\nrx_bytes=20\nerrno=0\n")
	if len(got) != 1 || got[0].PublicKey != pub || got[0].Tx != 10 || got[0].Rx != 20 || got[0].LastHandshake != 1700000000 {
		t.Errorf("stats = %+v", got)
	}
}

func TestNamesStayWithinLimits(t *testing.T) {
	for _, id := range []uint{1, 99, 4294967295} {
		for _, typ := range Types() {
			if n := len(IfaceName(typ, id)); n > 15 {
				t.Errorf("%s name for %d is %d bytes", typ, id, n)
			}
		}
		if len(TunName(id)) > 15 {
			t.Errorf("tun name for %d too long", id)
		}
	}
	seen := map[string]uint{}
	for id := uint(1); id < 300; id++ {
		a := TunAddress(id)
		if other, dup := seen[a]; dup {
			t.Fatalf("ids %d and %d share %s", other, id, a)
		}
		seen[a] = id
	}
	if TunAddress(1) != "10.254.0.5/30" {
		t.Errorf("TunAddress(1) = %s", TunAddress(1))
	}
}
