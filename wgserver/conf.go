package wgserver

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// ClientConfig is everything a client needs for one inbound.
type ClientConfig struct {
	// Conf is the wg-quick / awg-quick file.
	Conf string `json:"conf"`
	// URI is the wireguard:// share link (plain WireGuard inbounds only; the
	// AmneziaWG parameters have no place in that scheme).
	URI string `json:"uri,omitempty"`
	// Vpn is the Amnezia app's vpn:// link (AmneziaWG inbounds).
	Vpn string `json:"vpn,omitempty"`
	// Filename suggests a name for the downloaded .conf.
	Filename string `json:"filename"`
}

// HostPort joins the endpoint host with the inbound's port. An endpoint that
// already carries a port keeps it (a NAT or port forward may differ from the
// listen port).
func HostPort(endpoint string, port int) string {
	if endpoint == "" {
		return ""
	}
	if _, _, err := net.SplitHostPort(endpoint); err == nil {
		return endpoint
	}
	e := strings.Trim(endpoint, "[]")
	return net.JoinHostPort(e, strconv.Itoa(port))
}

// confValue rejects a value that would break out of its line in a .conf.
func confValue(v string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(v)
}

// BuildClient renders a client's config for an inbound. host is the endpoint
// host to use when the inbound has none of its own (the caller resolves it from
// the subscription domain, the panel domain or the server's address). name
// labels the profile.
func BuildClient(typ string, cfg Config, p Peer, host, name string) (ClientConfig, error) {
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = host
	}
	hp := HostPort(endpoint, cfg.ListenPort)
	if hp == "" {
		return ClientConfig{}, fmt.Errorf("no server address to put in the config: set the endpoint of the inbound")
	}
	addr, err := netip.ParseAddr(p.Address)
	if err != nil {
		return ClientConfig{}, fmt.Errorf("peer has no address")
	}
	mtu := cfg.EffectiveMTU(typ)

	var b strings.Builder
	label := confValue(name)
	if label != "" {
		fmt.Fprintf(&b, "# %s\n", label)
	}
	b.WriteString("[Interface]\n")
	fmt.Fprintf(&b, "PrivateKey = %s\n", p.PrivateKey)
	fmt.Fprintf(&b, "Address = %s/32\n", addr)
	if cfg.DNS != "" {
		fmt.Fprintf(&b, "DNS = %s\n", cfg.DNS)
	}
	fmt.Fprintf(&b, "MTU = %d\n", mtu)
	if typ == TypeAWG {
		writeAWG(&b, cfg)
	}
	b.WriteString("\n[Peer]\n")
	fmt.Fprintf(&b, "PublicKey = %s\n", cfg.PublicKey)
	if p.PresharedKey != "" {
		fmt.Fprintf(&b, "PresharedKey = %s\n", p.PresharedKey)
	}
	fmt.Fprintf(&b, "AllowedIPs = %s\n", cfg.AllowedIPs)
	fmt.Fprintf(&b, "Endpoint = %s\n", hp)
	if cfg.Keepalive > 0 {
		fmt.Fprintf(&b, "PersistentKeepalive = %d\n", cfg.Keepalive)
	}
	out := ClientConfig{Conf: b.String(), Filename: safeFilename(name, "wireguard") + ".conf"}
	if typ == TypeWG {
		out.URI = wireguardURI(cfg, p, addr, hp, mtu, name)
	} else {
		v, err := VpnURI(out.Conf)
		if err != nil {
			return out, err
		}
		out.Vpn = v
	}
	return out, nil
}

func safeFilename(name, fallback string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ' || r == '.':
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return fallback
	}
	s := b.String()
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

func writeAWG(b *strings.Builder, cfg Config) {
	o := cfg.Obfuscation.Prune(cfg.Version)
	f := Fields(cfg.Version)
	fmt.Fprintf(b, "Jc = %d\nJmin = %d\nJmax = %d\n", o.Jc, o.Jmin, o.Jmax)
	fmt.Fprintf(b, "S1 = %d\nS2 = %d\n", o.S1, o.S2)
	if f.S34 {
		fmt.Fprintf(b, "S3 = %d\nS4 = %d\n", o.S3, o.S4)
	}
	for i, h := range []string{o.H1, o.H2, o.H3, o.H4} {
		if h != "" {
			fmt.Fprintf(b, "H%d = %s\n", i+1, confValue(h))
		}
	}
	if f.CPS {
		for i, c := range []string{o.I1, o.I2, o.I3, o.I4, o.I5} {
			if c != "" {
				fmt.Fprintf(b, "I%d = %s\n", i+1, confValue(c))
			}
		}
	}
	if f.Protection {
		line := func(k, v string) {
			if v != "" {
				fmt.Fprintf(b, "%s = %s\n", k, confValue(v))
			}
		}
		line("HeaderProtectionKey", o.HeaderProtectionKey)
		line("ContentPaddingAddition", o.ContentPaddingAddition)
		line("RekeyAfterTime", o.RekeyAfterTime)
		line("RekeyTimeout", o.RekeyTimeout)
		line("RejectAfterTime", o.RejectAfterTime)
		line("KeepaliveTimeout", o.KeepaliveTimeout)
		line("MaxHandshakeAttempts", o.MaxHandshakeAttempts)
	}
	if f.TrailersCookie {
		fmt.Fprintf(b, "RandomTrailers = %t\nDisableCookies = %t\n", o.RandomTrailers, o.DisableCookies)
	}
}

// wireguardURI is the share link v2rayNG, Hiddify, NekoBox and friends import:
// wireguard://<private key>@host:port?publickey=..&address=..#name.
func wireguardURI(cfg Config, p Peer, addr netip.Addr, hostPort string, mtu int, name string) string {
	q := url.Values{}
	q.Set("publickey", cfg.PublicKey)
	if p.PresharedKey != "" {
		q.Set("presharedkey", p.PresharedKey)
	}
	q.Set("address", addr.String()+"/32")
	q.Set("mtu", strconv.Itoa(mtu))
	if cfg.DNS != "" {
		q.Set("dns", cfg.DNS)
	}
	if cfg.Keepalive > 0 {
		q.Set("keepalive", strconv.Itoa(cfg.Keepalive))
	}
	u := url.URL{Scheme: "wireguard", User: url.User(p.PrivateKey), Host: hostPort, RawQuery: q.Encode(), Fragment: name}
	return u.String()
}

// VpnURI encodes a client .conf as the Amnezia app's vpn:// link:
// "vpn://" + base64url(qCompress(JSON)), where qCompress is Qt's wrapper (a
// 4-byte big-endian length, then a zlib stream) and the JSON is Amnezia's
// container description with the config in last_config.
func VpnURI(conf string) (string, error) {
	meta := parseConf(conf)
	if meta.inner["client_priv_key"] == nil || meta.host == "" {
		return "", fmt.Errorf("config has no key or endpoint")
	}
	inner, err := json.Marshal(meta.inner)
	if err != nil {
		return "", err
	}
	awg := map[string]any{
		"last_config":        string(inner),
		"isThirdPartyConfig": true,
		"transport_proto":    "udp",
	}
	if meta.port > 0 {
		awg["port"] = strconv.Itoa(meta.port)
	}
	if v := protocolVersion(meta.inner); v != "" {
		awg["protocol_version"] = v
	}
	env := map[string]any{
		"defaultContainer": "amnezia-awg",
		"containers":       []any{map[string]any{"container": "amnezia-awg", "awg": awg}},
		"hostName":         meta.host,
	}
	if meta.desc != "" {
		env["description"] = meta.desc
	}
	if meta.dns1 != "" {
		env["dns1"] = meta.dns1
	}
	if meta.dns2 != "" {
		env["dns2"] = meta.dns2
	}
	payload, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(payload)))
	buf.Write(size[:])
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(payload); err != nil {
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	return "vpn://" + base64.RawURLEncoding.EncodeToString(buf.Bytes()), nil
}

type confMeta struct {
	host, dns1, dns2, desc string
	port                   int
	inner                  map[string]any
}

// confKeys are the [Interface] keys Amnezia's last_config carries under the
// same name with a capital first letter.
var confKeys = map[string]string{
	"jc": "Jc", "jmin": "Jmin", "jmax": "Jmax", "s1": "S1", "s2": "S2", "s3": "S3", "s4": "S4",
	"h1": "H1", "h2": "H2", "h3": "H3", "h4": "H4", "i1": "I1", "i2": "I2", "i3": "I3", "i4": "I4", "i5": "I5",
	"headerprotectionkey": "HeaderProtectionKey", "contentpaddingaddition": "ContentPaddingAddition",
	"rekeyaftertime": "RekeyAfterTime", "rekeytimeout": "RekeyTimeout", "rejectaftertime": "RejectAfterTime",
	"keepalivetimeout": "KeepaliveTimeout", "maxhandshakeattempts": "MaxHandshakeAttempts",
	"randomtrailers": "RandomTrailers", "disablecookies": "DisableCookies",
}

func parseConf(conf string) confMeta {
	m := confMeta{inner: map[string]any{"config": conf}}
	for _, raw := range strings.Split(conf, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "[") {
			continue
		}
		if strings.HasPrefix(line, "#") {
			if m.desc == "" {
				m.desc = strings.TrimSpace(strings.TrimPrefix(line, "#"))
			}
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		if val == "" {
			continue
		}
		switch strings.ToLower(key) {
		case "endpoint":
			if h, p, err := net.SplitHostPort(val); err == nil {
				m.host = h
				m.port, _ = strconv.Atoi(p)
				m.inner["hostName"] = h
				m.inner["port"] = m.port
			}
		case "dns":
			parts := splitList(val)
			if len(parts) > 0 {
				m.dns1 = parts[0]
			}
			if len(parts) > 1 {
				m.dns2 = parts[1]
			}
		case "privatekey":
			m.inner["client_priv_key"] = val
		case "address":
			m.inner["client_ip"] = val
		case "publickey":
			m.inner["server_pub_key"] = val
		case "presharedkey":
			m.inner["psk_key"] = val
		case "mtu":
			m.inner["mtu"] = val
		case "persistentkeepalive":
			m.inner["persistent_keep_alive"] = val
		case "allowedips":
			m.inner["allowed_ips"] = splitList(val)
		default:
			if k, ok := confKeys[strings.ToLower(key)]; ok {
				m.inner[k] = val
			}
		}
	}
	return m
}

// protocolVersion is what the Amnezia app uses to pick the client generation:
// without it older builds treat the config as version 1 and the handshake of a
// newer server dies.
func protocolVersion(inner map[string]any) string {
	set := func(k string) bool {
		s, _ := inner[k].(string)
		return strings.TrimSpace(s) != ""
	}
	for _, k := range []string{"HeaderProtectionKey", "ContentPaddingAddition", "RekeyAfterTime", "RekeyTimeout",
		"RejectAfterTime", "KeepaliveTimeout", "MaxHandshakeAttempts"} {
		if set(k) {
			return "3"
		}
	}
	for _, k := range []string{"S3", "S4"} {
		if n, _ := strconv.Atoi(strings.TrimSpace(fmt.Sprint(inner[k]))); n > 0 {
			return "2"
		}
	}
	for _, k := range []string{"H1", "H2", "H3", "H4"} {
		if s, _ := inner[k].(string); strings.Contains(s, "-") {
			return "2"
		}
	}
	if set("I1") {
		return "2"
	}
	return ""
}

// DecodeVpnURI reverses VpnURI; it exists for tests and diagnostics.
func DecodeVpnURI(uri string) (map[string]any, error) {
	s := strings.TrimPrefix(strings.TrimSpace(uri), "vpn://")
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	if len(raw) < 4 {
		return nil, fmt.Errorf("truncated")
	}
	zr, err := zlib.NewReader(bytes.NewReader(raw[4:]))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	var out map[string]any
	if err := json.NewDecoder(zr).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}
