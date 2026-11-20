package xray

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/samur005/3s-ui/database/model"
	"github.com/samur005/3s-ui/logger"

	"gorm.io/gorm"
)

// ClientCred holds a bound client's VLESS id and optional flow.
type ClientCred struct {
	Email  string
	ID     string
	Flow   string
	Enable bool
}

// BuildConfig writes a full Xray JSON config from every local enabled xvless
// inbound and their bound clients. outbounds are freedom+blackhole so traffic
// leaves the box directly; reverse portal/bridge is not implemented.
func BuildConfig(db *gorm.DB) ([]byte, error) {
	var rows []model.Inbound
	if err := db.Model(model.Inbound{}).
		Where("type = ? AND node_id IS NULL AND enable = ?", TypeVLESS, true).
		Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	inbounds := make([]map[string]any, 0, len(rows))
	outbounds := []map[string]any{
		{"protocol": "freedom", "tag": "direct"},
		{"protocol": "blackhole", "tag": "block"},
	}
	var routeRules []map[string]any
	for _, row := range rows {
		cfg, err := DecodeVless(row.Options)
		if err != nil {
			logger.Warningf("xray: inbound %s: %v", row.Tag, err)
			continue
		}
		clients, err := boundClients(db, row.Id)
		if err != nil {
			return nil, err
		}
		ib, err := buildInbound(row.Tag, cfg, clients)
		if err != nil {
			logger.Warningf("xray: inbound %s: %v", row.Tag, err)
			continue
		}
		inbounds = append(inbounds, ib)
		if cfg.RouteThroughCore {
			otag := HandoffOutboundTag(row.Id)
			outbounds = append(outbounds, socksOutbound(otag, row.Id, cfg))
			routeRules = append(routeRules, map[string]any{
				"type":        "field",
				"inboundTag":  []string{row.Tag},
				"outboundTag": otag,
			})
		}
	}
	doc := map[string]any{
		"log": map[string]any{
			"loglevel": "warning",
		},
		"inbounds":  inbounds,
		"outbounds": outbounds,
		// Panel route rules are NOT translated into Xray. When RouteThroughCore
		// is on, traffic is handed to sing-box over loopback SOCKS; otherwise
		// freedom/blackhole only. Reverse portal/bridge is not implemented.
		"routing": map[string]any{
			"domainStrategy": "AsIs",
			"rules":          routeRules,
		},
	}
	return json.MarshalIndent(doc, "", "  ")
}

func socksOutbound(tag string, id uint, c VlessConfig) map[string]any {
	server := map[string]any{
		"address": "127.0.0.1",
		"port":    HandoffPort(id),
	}
	if c.HandoffUser != "" {
		server["users"] = []map[string]any{{
			"user": c.HandoffUser,
			"pass": c.HandoffPass,
		}}
	}
	return map[string]any{
		"protocol": "socks",
		"tag":      tag,
		"settings": map[string]any{
			"servers": []any{server},
		},
	}
}

func buildInbound(tag string, c VlessConfig, clients []ClientCred) (map[string]any, error) {
	users := make([]map[string]any, 0, len(clients))
	for _, u := range clients {
		if !u.Enable || u.ID == "" {
			continue
		}
		flow := u.Flow
		if flow == "" {
			flow = c.FlowDefault
		}
		entry := map[string]any{"id": u.ID, "email": u.Email}
		if flow != "" {
			entry["flow"] = flow
		}
		users = append(users, entry)
	}
	settings := map[string]any{
		"clients":    users,
		"decryption": c.Decryption,
	}
	stream, err := streamSettings(c)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"tag":            tag,
		"listen":         c.Listen,
		"port":           c.ListenPort,
		"protocol":       "vless",
		"settings":       settings,
		"streamSettings": stream,
	}, nil
}

func streamSettings(c VlessConfig) (map[string]any, error) {
	ss := map[string]any{
		"method":   c.Method,
		"security": c.Security,
	}
	switch c.Method {
	case "raw":
		ss["rawSettings"] = map[string]any{}
	case "xhttp":
		ss["xhttpSettings"] = map[string]any{
			"path": c.Path,
			"host": c.Host,
			"mode": c.XHTTPMode,
		}
	case "mkcp":
		ss["kcpSettings"] = map[string]any{
			"mtu":              c.KcpMTU,
			"tti":              c.KcpTTI,
			"uplinkCapacity":   c.KcpUplinkCapacity,
			"downlinkCapacity": c.KcpDownlinkCapacity,
		}
	case "grpc":
		ss["grpcSettings"] = map[string]any{
			"serviceName": c.ServiceName,
		}
	case "websocket":
		ss["wsSettings"] = map[string]any{
			"path": c.Path,
			"host": c.Host,
		}
	case "httpupgrade":
		ss["httpupgradeSettings"] = map[string]any{
			"path": c.Path,
			"host": c.Host,
		}
	}
	if c.FinalMaskType != "" {
		fm := map[string]any{"type": c.FinalMaskType}
		if c.FinalMaskPassword != "" {
			fm["settings"] = map[string]any{"password": c.FinalMaskPassword}
		}
		ss["finalmask"] = []any{fm}
	}
	switch c.Security {
	case "tls":
		ss["tlsSettings"] = map[string]any{
			"certificates": []map[string]any{{
				"certificateFile": c.CertFile,
				"keyFile":         c.KeyFile,
			}},
		}
	case "reality":
		ss["realitySettings"] = map[string]any{
			"target":      c.RealityDest,
			"serverNames": c.RealityServerNames,
			"privateKey":  c.RealityPrivateKey,
			"shortIds":    c.RealityShortIds,
		}
	}
	return ss, nil
}

// boundClients lists clients whose inbounds JSON contains the inbound id.
func boundClients(db *gorm.DB, inboundID uint) ([]ClientCred, error) {
	var clients []model.Client
	if err := db.Model(model.Client{}).Find(&clients).Error; err != nil {
		return nil, err
	}
	out := make([]ClientCred, 0)
	idStr := fmt.Sprintf("%d", inboundID)
	for _, c := range clients {
		if !clientBound(c.Inbounds, inboundID) {
			continue
		}
		cred := ClientCred{Email: c.Name, Enable: c.Enable}
		cred.ID, cred.Flow = clientVlessCred(c.Config)
		if cred.ID == "" {
			logger.Warningf("xray: client %s has no VLESS uuid", c.Name)
			continue
		}
		out = append(out, cred)
		_ = idStr
	}
	return out, nil
}

func clientBound(raw json.RawMessage, id uint) bool {
	var ids []uint
	if json.Unmarshal(raw, &ids) != nil {
		return false
	}
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// Prefer the xvless block, then the shared vless uuid so a client already
// using sing-box VLESS keeps one id.
func clientVlessCred(raw json.RawMessage) (id, flow string) {
	var m map[string]map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return "", ""
	}
	for _, key := range []string{"xvless", "vless"} {
		if b, ok := m[key]; ok {
			if s, _ := b["uuid"].(string); s != "" {
				id = s
			}
			if s, _ := b["flow"].(string); s != "" {
				flow = s
			}
			if id != "" {
				return id, flow
			}
		}
	}
	return "", ""
}

// WriteConfigFile puts the config under dir/config.json.
func WriteConfigFile(dir string, raw []byte) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// ConfigDir is where the panel keeps the generated Xray config.
func ConfigDir() string {
	if p := strings.TrimSpace(os.Getenv("S3UI_XRAY_DIR")); p != "" {
		return p
	}
	return "db/xray"
}

// BuildInboundForTest exposes buildInbound for integration tests in other packages.
func BuildInboundForTest(tag string, c VlessConfig, uuid string) (map[string]any, error) {
	return buildInbound(tag, c, []ClientCred{{Email: "u", ID: uuid, Enable: true}})
}
