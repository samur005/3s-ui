package xray

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Transport methods VLESS supports in current Xray (streamSettings.method).
// Official docs: raw | xhttp | mkcp | grpc | websocket | httpupgrade | hysteria | masque.
// This panel exposes the ones asked for; hysteria/masque are Xray-native protocols of
// their own and are not offered as a VLESS transport here.
var VLESSMethods = []string{"raw", "xhttp", "mkcp", "grpc", "websocket", "httpupgrade"}

// Security layers VLESS streamSettings.security accepts.
var VLESSSecurities = []string{"none", "tls", "reality"}

// XHTTP modes from Xray's XHTTPObject (discussion #4113 / current core).
var XHTTPModes = []string{"auto", "packet-up", "stream-up", "stream-one"}

// Reality is only valid with these methods (official transport docs).
var RealityMethods = map[string]bool{"raw": true, "xhttp": true, "grpc": true}

// Vision (xtls-rprx-vision) is only for TCP(raw)+TLS/REALITY, or with VLESS Encryption.
const FlowVision = "xtls-rprx-vision"

// VlessConfig is the stored shape of an xvless inbound's options.
type VlessConfig struct {
	Listen     string `json:"listen"`
	ListenPort int    `json:"listen_port"`

	// Endpoint is the host (or host:port) put in client share links.
	Endpoint string `json:"endpoint"`

	Method   string `json:"method"`   // streamSettings.method
	Security string `json:"security"` // none | tls | reality

	// Shared transport fields.
	Path        string `json:"path"`
	Host        string `json:"host"`
	ServiceName string `json:"serviceName"` // gRPC
	XHTTPMode   string `json:"xhttpMode"`   // XHTTP mode

	// mKCP (kcpSettings). Header/seed were removed in current Xray; use FinalMask instead.
	KcpMTU              int `json:"kcpMtu"`
	KcpTTI              int `json:"kcpTti"`
	KcpUplinkCapacity   int `json:"kcpUplinkCapacity"`
	KcpDownlinkCapacity int `json:"kcpDownlinkCapacity"`

	// FinalMask for mKCP (and optional elsewhere): type "salamander" + password,
	// or empty to leave unset. Official docs removed legacy kcp header/seed.
	FinalMaskType     string `json:"finalMaskType"`
	FinalMaskPassword string `json:"finalMaskPassword"`

	// TLS (when security=tls). Paths on the host; not the panel's sing-box TLS table.
	CertFile   string   `json:"certFile"`
	KeyFile    string   `json:"keyFile"`
	ServerName string   `json:"serverName"`
	Alpn       []string `json:"alpn"`

	// REALITY (when security=reality).
	RealityDest        string   `json:"realityDest"`
	RealityServerNames []string `json:"realityServerNames"`
	RealityPrivateKey  string   `json:"realityPrivateKey"`
	RealityPublicKey   string   `json:"realityPublicKey"` // for client links
	RealityShortIds    []string `json:"realityShortIds"`
	RealityFingerprint string   `json:"realityFingerprint"`
	RealitySpiderX     string   `json:"realitySpiderX"`

	// VLESS Encryption: server "decryption" and matching client "encryption"
	// from `xray vlessenc`. "none" disables. Fallbacks cannot be used with it.
	Decryption string `json:"decryption"`
	Encryption string `json:"encryption"` // stored so share links can carry it

	// FlowDefault is applied to clients that do not set their own flow.
	// Vision is only valid with raw+tls/reality, or when Encryption is set.
	FlowDefault string `json:"flow"`

	// RouteThroughCore sends decrypted traffic into a loopback sing-box mixed
	// inbound (same tag as this row) so panel routing/outbounds apply.
	// Default true. Off = Xray freedom outbound (no panel rules).
	RouteThroughCore bool   `json:"routeThroughCore"`
	OutboundTag      string `json:"outboundTag"`
	// HandoffUser/Pass authenticate the loopback SOCKS/mixed handoff. Generated
	// once; not shown in the UI and never put in share links.
	HandoffUser string `json:"handoffUser,omitempty"`
	HandoffPass string `json:"handoffPass,omitempty"`
	HandoffPort int    `json:"handoffPort,omitempty"`
}

func DecodeVless(raw json.RawMessage) (VlessConfig, error) {
	var c VlessConfig
	if len(raw) == 0 || string(raw) == "null" {
		return c, nil
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("invalid options: %w", err)
	}
	return c, nil
}

func (c VlessConfig) Encode() (json.RawMessage, error) {
	return json.Marshal(c)
}

func oneOf(v string, list []string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// NormalizeVless fills defaults and validates against official Xray rules.
func NormalizeVless(id uint, options, stored json.RawMessage) (json.RawMessage, error) {
	prev, _ := DecodeVless(stored)
	c, err := DecodeVless(options)
	if err != nil {
		return nil, err
	}
	if !jsonHasKey(options, "routeThroughCore") {
		if jsonHasKey(stored, "routeThroughCore") {
			c.RouteThroughCore = prev.RouteThroughCore
		} else {
			c.RouteThroughCore = true
		}
	}
	if jsonHasKey(options, "outboundTag") {
		c.OutboundTag = strings.TrimSpace(c.OutboundTag)
	} else {
		c.OutboundTag = strings.TrimSpace(prev.OutboundTag)
	}
	if strings.ContainsAny(c.OutboundTag, "\r\n\t") {
		return nil, fmt.Errorf("outboundTag must not contain whitespace control characters")
	}
	if err := AllocateHandoffFields(&c, prev, id); err != nil {
		return nil, err
	}
	// Merge: empty string/0 from the form keeps the stored value for keys the
	// operator is not editing. Port 0 means "unset" and falls back.
	if c.Listen == "" {
		c.Listen = prev.Listen
	}
	if c.ListenPort == 0 {
		c.ListenPort = prev.ListenPort
	}
	if c.Endpoint == "" {
		c.Endpoint = prev.Endpoint
	}
	if c.Method == "" {
		c.Method = prev.Method
	}
	if c.Security == "" {
		c.Security = prev.Security
	}
	if c.Decryption == "" {
		c.Decryption = prev.Decryption
	}
	if c.Encryption == "" && c.Decryption == prev.Decryption {
		c.Encryption = prev.Encryption
	}
	if c.RealityPrivateKey == "" {
		c.RealityPrivateKey = prev.RealityPrivateKey
		if c.RealityPublicKey == "" {
			c.RealityPublicKey = prev.RealityPublicKey
		}
	}

	if c.Listen == "" {
		c.Listen = "0.0.0.0"
	}
	if c.ListenPort < 1 || c.ListenPort > 65535 {
		return nil, fmt.Errorf("listen_port must be 1..65535")
	}
	if c.Method == "" {
		c.Method = "raw"
	}
	// Accept the old share-link name "tcp" as raw.
	if c.Method == "tcp" {
		c.Method = "raw"
	}
	if !oneOf(c.Method, VLESSMethods) {
		return nil, fmt.Errorf("method %q is not one of %s", c.Method, strings.Join(VLESSMethods, ", "))
	}
	if c.Security == "" {
		c.Security = "none"
	}
	if !oneOf(c.Security, VLESSSecurities) {
		return nil, fmt.Errorf("security %q is not one of %s", c.Security, strings.Join(VLESSSecurities, ", "))
	}
	if c.Security == "reality" && !RealityMethods[c.Method] {
		return nil, fmt.Errorf("REALITY only works with raw, xhttp or grpc (not %s)", c.Method)
	}
	if c.Decryption == "" {
		c.Decryption = "none"
	}
	if c.Decryption != "none" && c.Encryption == "" {
		return nil, fmt.Errorf("VLESS Encryption needs both decryption (server) and encryption (client); generate them with xray vlessenc")
	}
	// Public network without TLS/REALITY/Encryption is refused by Xray for VLESS.
	if c.Security == "none" && c.Decryption == "none" {
		// Still allow: operator may bind a private address. Warn via validation
		// only when listen is a public-looking all-interfaces bind — we allow
		// it but document the restriction; Xray itself enforces peer private.
	}
	if c.FlowDefault != "" && c.FlowDefault != FlowVision {
		return nil, fmt.Errorf("flow %q is not supported (only %q or empty)", c.FlowDefault, FlowVision)
	}
	if c.FlowDefault == FlowVision {
		ok := (c.Method == "raw" && (c.Security == "tls" || c.Security == "reality")) || c.Decryption != "none"
		if !ok {
			return nil, fmt.Errorf("Vision (xtls-rprx-vision) needs raw+tls/reality, or VLESS Encryption")
		}
	}
	if c.Method == "xhttp" {
		if c.XHTTPMode == "" {
			c.XHTTPMode = "auto"
		}
		if !oneOf(c.XHTTPMode, XHTTPModes) {
			return nil, fmt.Errorf("xhttpMode %q is not one of %s", c.XHTTPMode, strings.Join(XHTTPModes, ", "))
		}
		if c.Path == "" {
			c.Path = "/"
		}
	}
	if c.Method == "mkcp" {
		if c.KcpMTU == 0 {
			c.KcpMTU = 1350
		}
		if c.KcpTTI == 0 {
			c.KcpTTI = 50
		}
		if c.KcpUplinkCapacity == 0 {
			c.KcpUplinkCapacity = 5
		}
		if c.KcpDownlinkCapacity == 0 {
			c.KcpDownlinkCapacity = 20
		}
		if c.KcpMTU < 576 || c.KcpMTU > 1460 {
			return nil, fmt.Errorf("kcp mtu must be 576..1460")
		}
	}
	if c.Security == "tls" {
		if c.CertFile == "" || c.KeyFile == "" {
			return nil, fmt.Errorf("tls needs certFile and keyFile")
		}
	}
	if c.Security == "reality" {
		if c.RealityDest == "" {
			return nil, fmt.Errorf("reality needs realityDest (target), e.g. www.cloudflare.com:443")
		}
		if c.RealityPrivateKey == "" {
			return nil, fmt.Errorf("reality needs realityPrivateKey (from xray x25519)")
		}
		if len(c.RealityServerNames) == 0 {
			return nil, fmt.Errorf("reality needs at least one realityServerNames entry")
		}
		if len(c.RealityShortIds) == 0 {
			c.RealityShortIds = []string{""}
		}
		if c.RealityFingerprint == "" {
			c.RealityFingerprint = "chrome"
		}
	}
	c.Endpoint = strings.TrimSpace(c.Endpoint)
	c.Path = strings.TrimSpace(c.Path)
	c.Host = strings.TrimSpace(c.Host)
	c.ServiceName = strings.TrimSpace(c.ServiceName)
	return c.Encode()
}

// HostPort for share links.
func (c VlessConfig) HostPort() (string, int, error) {
	host := strings.TrimSpace(c.Endpoint)
	port := c.ListenPort
	if host == "" {
		return "", 0, fmt.Errorf("set the endpoint of the inbound for client links")
	}
	if h, p, err := net.SplitHostPort(host); err == nil {
		host = h
		if n, e := strconv.Atoi(p); e == nil {
			port = n
		}
	}
	return host, port, nil
}

func jsonHasKey(raw json.RawMessage, key string) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return false
	}
	_, ok := m[key]
	return ok
}
