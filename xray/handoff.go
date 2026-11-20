package xray

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/samur005/3s-ui/database"

	"gorm.io/gorm"
)

// Handoff is how decrypted Xray traffic reaches sing-box: one loopback-only
// mixed inbound per enabled xvless that has RouteThroughCore, so panel route
// rules (by inbound tag) and outbounds apply. This is not a translation of
// panel rules into Xray JSON.
//
// Per-client SOCKS usernames (panel client names) are mapped so sing-box
// auth_user rules work. Original client IP cannot be preserved over loopback
// SOCKS (always 127.0.0.1); see identity.go.
//
// The mixed inbound reuses the xvless row's tag so operator inbound-tag rules
// match. Tags are unique across all panel inbound rows (tagFreeAcross), and
// GetAllConfig already excludes xvless, so the injected mixed does not collide
// with a sing-box inbound of the same tag under normal saves.

const (
	handoffPortBase = 41200
	handoffPortSpan = 8000 // 41200..49199
)

var (
	coreMu sync.Mutex
	coreFP string
)

// HandoffPort returns the stored loopback port, or a deterministic fallback
// from id when older rows lack handoffPort (should be filled by Normalize).
func HandoffPort(id uint, stored int) int {
	if stored >= handoffPortBase && stored < handoffPortBase+handoffPortSpan {
		return stored
	}
	if id == 0 {
		return handoffPortBase
	}
	return handoffPortBase + int(id%uint(handoffPortSpan))
}

// HandoffOutboundTag is the Xray outbound tag that dials the core handoff.
func HandoffOutboundTag(id uint) string {
	return fmt.Sprintf("s3ui-xray-core-%d", id)
}

func randomSecret(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func ensureHandoffAuth(c *VlessConfig, prev VlessConfig) {
	if c.HandoffUser == "" {
		c.HandoffUser = prev.HandoffUser
	}
	if c.HandoffPass == "" {
		c.HandoffPass = prev.HandoffPass
	}
	if c.HandoffUser == "" {
		c.HandoffUser = "xray-" + randomSecret(4)
	}
	if c.HandoffPass == "" {
		c.HandoffPass = randomSecret(16)
	}
}

// ensureHandoffPort keeps a stable port in options. It avoids colliding with
// other xvless handoffs and with this inbound's own public listen_port when
// that listen is on all interfaces or loopback.
func ensureHandoffPort(c *VlessConfig, prev VlessConfig, id uint, others []handoffRow) error {
	if c.HandoffPort == 0 {
		c.HandoffPort = prev.HandoffPort
	}
	taken := map[int]bool{}
	for _, o := range others {
		if o.ID == id {
			continue
		}
		oc, err := DecodeVless(o.Options)
		if err != nil {
			continue
		}
		taken[HandoffPort(o.ID, oc.HandoffPort)] = true
		// Also reserve the sibling's public listen port if it binds loopback/all.
		if oc.ListenPort > 0 && listenConflictsWithHandoff(oc.Listen) {
			taken[oc.ListenPort] = true
		}
	}
	if c.ListenPort > 0 && listenConflictsWithHandoff(c.Listen) {
		taken[c.ListenPort] = true
	}
	port := c.HandoffPort
	if port >= handoffPortBase && port < handoffPortBase+handoffPortSpan && !taken[port] && portFreeOnLoopback(port) {
		c.HandoffPort = port
		return nil
	}
	// Prefer id-based slot, then scan the span.
	candidates := []int{}
	if id != 0 {
		candidates = append(candidates, handoffPortBase+int(id%uint(handoffPortSpan)))
	}
	for i := 0; i < handoffPortSpan; i++ {
		candidates = append(candidates, handoffPortBase+i)
	}
	for _, p := range candidates {
		if taken[p] {
			continue
		}
		if !portFreeOnLoopback(p) {
			continue
		}
		c.HandoffPort = p
		return nil
	}
	return fmt.Errorf("no free loopback handoff port in %d–%d", handoffPortBase, handoffPortBase+handoffPortSpan-1)
}

func listenConflictsWithHandoff(listen string) bool {
	l := strings.TrimSpace(listen)
	return l == "" || l == "0.0.0.0" || l == "::" || l == "127.0.0.1" || l == "::1"
}

func portFreeOnLoopback(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

type handoffRow struct {
	ID      uint
	Tag     string
	Enable  bool
	Options json.RawMessage
}

func loadHandoffRows(db *gorm.DB) ([]handoffRow, error) {
	if db == nil {
		return nil, fmt.Errorf("no database")
	}
	var rows []handoffRow
	err := db.Table("inbounds").
		Select("id", "tag", "enable", "options").
		Where("type = ? AND node_id IS NULL", TypeVLESS).
		Order("id").Find(&rows).Error
	return rows, err
}

// AllocateHandoffFields fills auth+port during Normalize (id may be 0 for new).
func AllocateHandoffFields(c *VlessConfig, prev VlessConfig, id uint) error {
	ensureHandoffAuth(c, prev)
	if !c.RouteThroughCore {
		// Keep prior port/auth so toggling back on is stable.
		if c.HandoffPort == 0 {
			c.HandoffPort = prev.HandoffPort
		}
		return nil
	}
	others, err := loadHandoffRows(database.GetDB())
	if err != nil {
		others = nil
	}
	return ensureHandoffPort(c, prev, id, others)
}

func coreFragment(rows []handoffRow) (mixed []json.RawMessage, rules []json.RawMessage, err error) {
	var tags []string
	var forced []json.RawMessage
	ports := map[int]string{}
	db := database.GetDB()
	for _, row := range rows {
		if !row.Enable {
			continue
		}
		cfg, err := DecodeVless(row.Options)
		if err != nil || !cfg.RouteThroughCore {
			continue
		}
		tag := strings.TrimSpace(row.Tag)
		if tag == "" {
			continue
		}
		if cfg.HandoffUser == "" || cfg.HandoffPass == "" {
			return nil, nil, fmt.Errorf("xray handoff: inbound %q missing SOCKS credentials", tag)
		}
		port := HandoffPort(row.ID, cfg.HandoffPort)
		if other, ok := ports[port]; ok {
			return nil, nil, fmt.Errorf("xray handoff: port %d used by %q and %q", port, other, tag)
		}
		ports[port] = tag

		var users []map[string]any
		if db != nil {
			clients, cerr := boundClients(db, row.ID)
			if cerr != nil {
				return nil, nil, cerr
			}
			for _, cs := range clientSocksList(row.ID, cfg.HandoffPass, clients) {
				users = append(users, map[string]any{
					"username": cs.Username,
					"password": cs.Password,
				})
			}
		}
		// Fallback shared account when no clients are bound yet (inbound idle).
		if len(users) == 0 {
			users = []map[string]any{{
				"username": cfg.HandoffUser,
				"password": cfg.HandoffPass,
			}}
		}

		obj := map[string]any{
			"type":        "mixed",
			"tag":         tag,
			"listen":      "127.0.0.1",
			"listen_port": port,
			"users":       users,
		}
		raw, _ := json.Marshal(obj)
		mixed = append(mixed, raw)
		tags = append(tags, tag)
		if ot := strings.TrimSpace(cfg.OutboundTag); ot != "" {
			r, _ := json.Marshal(map[string]any{
				"inbound": []string{tag}, "action": "route", "outbound": ot,
			})
			forced = append(forced, r)
		}
	}
	if len(mixed) == 0 {
		return nil, nil, nil
	}
	sniff, _ := json.Marshal(map[string]any{"inbound": tags, "action": "sniff"})
	rules = append([]json.RawMessage{sniff}, forced...)
	return mixed, rules, nil
}

func fingerprint(rows []handoffRow) string {
	mixed, rules, err := coreFragment(rows)
	if err != nil {
		return "err:" + err.Error()
	}
	var b strings.Builder
	for _, m := range mixed {
		b.Write(m)
	}
	for _, r := range rules {
		b.Write(r)
	}
	return b.String()
}

// InjectCore adds loopback mixed handoffs and sniff/force-outbound rules to a
// sing-box config about to start. Injected listeners are not stored as panel
// inbounds and never appear in subscriptions.
func InjectCore(inbounds []json.RawMessage, route json.RawMessage) ([]json.RawMessage, json.RawMessage, error) {
	rows, err := loadHandoffRows(database.GetDB())
	if err != nil {
		return inbounds, route, nil
	}
	return injectInto(rows, inbounds, route)
}

func injectInto(rows []handoffRow, inbounds []json.RawMessage, route json.RawMessage) ([]json.RawMessage, json.RawMessage, error) {
	mixed, rules, err := coreFragment(rows)
	if err != nil {
		return inbounds, route, err
	}
	coreMu.Lock()
	coreFP = fingerprint(rows)
	coreMu.Unlock()
	if len(mixed) == 0 {
		return inbounds, route, nil
	}
	taken := map[string]bool{}
	for _, raw := range inbounds {
		var probe struct {
			Tag string `json:"tag"`
		}
		if json.Unmarshal(raw, &probe) == nil && probe.Tag != "" {
			taken[probe.Tag] = true
		}
	}
	for _, m := range mixed {
		var probe struct {
			Tag string `json:"tag"`
		}
		_ = json.Unmarshal(m, &probe)
		if taken[probe.Tag] {
			return inbounds, route, fmt.Errorf("xray handoff: an inbound already uses tag %q", probe.Tag)
		}
	}
	out := make([]json.RawMessage, 0, len(inbounds)+len(mixed))
	out = append(out, inbounds...)
	out = append(out, mixed...)

	r := map[string]json.RawMessage{}
	if len(route) > 0 && string(route) != "null" {
		if err := json.Unmarshal(route, &r); err != nil {
			return inbounds, route, fmt.Errorf("xray handoff: route section: %w", err)
		}
	}
	var existing []json.RawMessage
	if raw, ok := r["rules"]; ok {
		_ = json.Unmarshal(raw, &existing)
	}
	merged := append(append([]json.RawMessage{}, rules...), existing...)
	r["rules"], _ = json.Marshal(merged)
	newRoute, err := json.Marshal(r)
	if err != nil {
		return inbounds, route, err
	}
	return out, newRoute, nil
}

// CoreNeedsRestart reports whether the running core was built with a different
// Xray handoff fragment than the DB implies.
func CoreNeedsRestart() bool {
	rows, err := loadHandoffRows(database.GetDB())
	if err != nil {
		return false
	}
	coreMu.Lock()
	defer coreMu.Unlock()
	return coreFP != fingerprint(rows)
}

// AnyRouteThroughCore reports whether any enabled xvless hands off to sing-box.
func AnyRouteThroughCore(db *gorm.DB) bool {
	rows, err := loadHandoffRows(db)
	if err != nil {
		return false
	}
	for _, row := range rows {
		if !row.Enable {
			continue
		}
		cfg, err := DecodeVless(row.Options)
		if err == nil && cfg.RouteThroughCore {
			return true
		}
	}
	return false
}

// ResetCoreFingerprintForTest clears the handoff fingerprint so CoreNeedsRestart
// reports dirty. Tests only.
func ResetCoreFingerprintForTest() {
	coreMu.Lock()
	coreFP = ""
	coreMu.Unlock()
}
