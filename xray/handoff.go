package xray

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
// SOCKS cannot carry the original client IP or the VLESS user email into
// sing-box; rules that depend on those see 127.0.0.1 and no user.

const (
	handoffPortBase = 41200
	handoffPortSpan = 8000 // ports 41200..49199
)

var (
	coreMu sync.Mutex
	coreFP string
)

// HandoffPort is a stable loopback port for inbound id.
func HandoffPort(id uint) int {
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

// ensureHandoffAuth fills SOCKS credentials once; kept across edits.
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

type handoffRow struct {
	ID      uint
	Tag     string
	Enable  bool
	Options json.RawMessage
}

func loadHandoffRows(db *gorm.DB) ([]handoffRow, error) {
	var rows []handoffRow
	err := db.Table("inbounds").
		Select("id", "tag", "enable", "options").
		Where("type = ? AND node_id IS NULL", TypeVLESS).
		Order("id").Find(&rows).Error
	return rows, err
}

// coreFragment builds the sing-box mixed inbounds and leading route rules for
// every enabled xvless that routes through the core. The mixed inbound reuses
// the panel inbound's tag so operator rules that match on that inbound apply.
func coreFragment(rows []handoffRow) (mixed []json.RawMessage, rules []json.RawMessage) {
	var tags []string
	var forced []json.RawMessage
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
		port := HandoffPort(row.ID)
		obj := map[string]any{
			"type":        "mixed",
			"tag":         tag,
			"listen":      "127.0.0.1",
			"listen_port": port,
			// Auth stops other local processes from using the handoff; the
			// listener is loopback-only as the primary boundary.
			"users": []map[string]any{{
				"username": cfg.HandoffUser,
				"password": cfg.HandoffPass,
			}},
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
		return nil, nil
	}
	sniff, _ := json.Marshal(map[string]any{"inbound": tags, "action": "sniff"})
	rules = append([]json.RawMessage{sniff}, forced...)
	return mixed, rules
}

func fingerprint(rows []handoffRow) string {
	mixed, rules := coreFragment(rows)
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
	mixed, rules := coreFragment(rows)
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
