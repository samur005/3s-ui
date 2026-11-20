package xray

import (
	"fmt"
	"net/url"
	"strings"
)

// ShareLink builds a vless:// URI for one client on one inbound, using the
// query parameters common clients understand (type= for the transport name in
// the share-link dialect, which still says "tcp" for raw).
func ShareLink(cfg VlessConfig, uuid, remark string) (string, error) {
	host, port, err := cfg.HostPort()
	if err != nil {
		return "", err
	}
	q := url.Values{}
	q.Set("encryption", "none")
	if cfg.Encryption != "" && cfg.Decryption != "none" {
		q.Set("encryption", cfg.Encryption)
	}
	typ := cfg.Method
	if typ == "raw" {
		typ = "tcp"
	}
	if typ == "websocket" {
		typ = "ws"
	}
	q.Set("type", typ)
	q.Set("security", cfg.Security)
	switch cfg.Method {
	case "xhttp":
		q.Set("path", cfg.Path)
		if cfg.Host != "" {
			q.Set("host", cfg.Host)
		}
		if cfg.XHTTPMode != "" && cfg.XHTTPMode != "auto" {
			q.Set("mode", cfg.XHTTPMode)
		}
	case "websocket", "httpupgrade":
		if cfg.Path != "" {
			q.Set("path", cfg.Path)
		}
		if cfg.Host != "" {
			q.Set("host", cfg.Host)
		}
	case "grpc":
		if cfg.ServiceName != "" {
			q.Set("serviceName", cfg.ServiceName)
		}
	case "mkcp":
		// header/seed removed in current Xray; FinalMask is not in share links yet.
	}
	switch cfg.Security {
	case "tls":
		if cfg.ServerName != "" {
			q.Set("sni", cfg.ServerName)
		}
		if len(cfg.Alpn) > 0 {
			q.Set("alpn", strings.Join(cfg.Alpn, ","))
		}
	case "reality":
		if len(cfg.RealityServerNames) > 0 {
			q.Set("sni", cfg.RealityServerNames[0])
		}
		if cfg.RealityPublicKey != "" {
			q.Set("pbk", cfg.RealityPublicKey)
		}
		if len(cfg.RealityShortIds) > 0 {
			q.Set("sid", cfg.RealityShortIds[0])
		}
		if cfg.RealityFingerprint != "" {
			q.Set("fp", cfg.RealityFingerprint)
		}
		if cfg.RealitySpiderX != "" {
			q.Set("spx", cfg.RealitySpiderX)
		}
	}
	if cfg.FlowDefault != "" {
		q.Set("flow", cfg.FlowDefault)
	}
	u := url.URL{
		Scheme:   "vless",
		User:     url.User(uuid),
		Host:     fmt.Sprintf("%s:%d", host, port),
		RawQuery: q.Encode(),
		Fragment: remark,
	}
	return u.String(), nil
}
