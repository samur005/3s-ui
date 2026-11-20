package qwdtt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// InboundType is the type of the inbound row qWDTT lives in. sing-box knows no
// such listener: the row is the panel's own record of the qWDTT server (its
// settings, its on/off switch, the clients that get its link) and is kept out
// of every config handed to the core.
const InboundType = "qwdtt"

// storeMu serializes read-modify-write cycles on the inbound row and the state
// row; the panel's API handlers and the cron jobs both write them.
var storeMu sync.Mutex

func loadRow(db *gorm.DB) (model.QwdttState, error) {
	var row model.QwdttState
	err := db.Where("id = ?", 1).First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return model.QwdttState{Id: 1}, nil
	}
	return row, err
}

func saveRow(db *gorm.DB, row model.QwdttState) error {
	row.Id = 1
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		UpdateAll: true,
	}).Create(&row).Error
}

// LoadInbound returns the qWDTT inbound row, or nil when there is none. A
// replica of a node's inbound is not it: that one runs on its node.
func LoadInbound(db *gorm.DB) (*model.Inbound, error) {
	var rows []model.Inbound
	err := db.Model(model.Inbound{}).
		Where("type = ? AND node_id IS NULL", InboundType).
		Order("id").Limit(1).Find(&rows).Error
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

// InboundID is the id of the qWDTT inbound, 0 when there is none.
func InboundID() uint {
	in, err := LoadInbound(database.GetDB())
	if err != nil || in == nil {
		return 0
	}
	return in.Id
}

// Load returns the configuration of the qWDTT inbound merged over the
// defaults. With no inbound it returns Default(): disabled, no password yet.
// The inbound's own switch is the Enabled flag, so a disabled row stops the
// server like an absent one does.
func Load() (Config, error) {
	in, err := LoadInbound(database.GetDB())
	if err != nil {
		return Config{}, err
	}
	if in == nil {
		return Default(), nil
	}
	cfg := decodeConfig(string(in.Options))
	cfg.Enabled = in.Enable
	return cfg, nil
}

func decodeConfig(raw string) Config {
	cfg := Default()
	if strings.TrimSpace(raw) != "" {
		var stored map[string]json.RawMessage
		if json.Unmarshal([]byte(raw), &stored) == nil {
			_ = json.Unmarshal([]byte(raw), &cfg)
			// A boolean that defaults to true must stay true for rows written
			// before the key existed.
			if _, ok := stored["autoVkHash"]; !ok {
				cfg.AutoVkHash = true
			}
			if _, ok := stored["routeThroughCore"]; !ok {
				cfg.RouteThroughCore = true
			}
		}
	}
	return cfg.Merge()
}

// normalize cleans a configuration for storing and validates it. A missing
// password is generated.
func normalize(cfg Config) (Config, error) {
	cfg = cfg.Merge()
	cfg.VkHashes = NormalizeHashes(cfg.VkHashes)
	cfg.SubHost = strings.TrimSpace(cfg.SubHost)
	cfg.OutboundTag = strings.TrimSpace(cfg.OutboundTag)
	cfg.Remark = strings.TrimSpace(cfg.Remark)
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg.EnsurePassword()
}

// options is how a configuration sits in the inbound row's options column:
// the JSON of Config without enabled (the row's own switch is that). Port and
// address are also written as listen / listen_port, which is where the
// inbounds list reads them from.
func (c Config) options() json.RawMessage {
	var m map[string]any
	_ = json.Unmarshal([]byte(c.encode()), &m)
	delete(m, "enabled")
	if host, port, err := net.SplitHostPort(c.ListenAddr); err == nil {
		m["listen"] = host
		if p, err := strconv.Atoi(port); err == nil {
			m["listen_port"] = p
		}
	}
	b, _ := json.Marshal(m)
	return b
}

// NormalizeOptions is what the inbounds save runs on a qwdtt inbound. The
// options as the form sent them are laid over the stored ones (nil for a new
// inbound), so a key the form leaves out -- the password, the hashes the panel
// generated meanwhile -- keeps its value; the result is cleaned, validated and
// written back in the stored shape, so nothing the server does not know stays
// in the row.
func NormalizeOptions(options, stored json.RawMessage) (json.RawMessage, Config, error) {
	cfg := decodeConfig(string(stored))
	if len(options) > 0 {
		if err := json.Unmarshal(options, &cfg); err != nil {
			return nil, cfg, fmt.Errorf("qwdtt: invalid settings: %w", err)
		}
	}
	cfg, err := normalize(cfg)
	if err != nil {
		return nil, cfg, err
	}
	return cfg.options(), cfg, nil
}

// OtherInboundExists reports whether a qWDTT inbound other than id is stored.
// One server runs per host, so a second one is refused.
func OtherInboundExists(db *gorm.DB, id uint) (bool, error) {
	var n int64
	err := db.Model(model.Inbound{}).
		Where("type = ? AND node_id IS NULL AND id <> ?", InboundType, id).Count(&n).Error
	return n > 0, err
}

// uniqueTag finds a tag no inbound, outbound, endpoint or service holds.
func uniqueTag(db *gorm.DB) string {
	for i := 0; i < 50; i++ {
		tag := "qwdtt"
		if i > 0 {
			tag = "qwdtt-" + strconv.Itoa(i)
		}
		var n int64
		db.Model(model.Inbound{}).Where("tag = ?", tag).Count(&n)
		if n == 0 {
			return tag
		}
	}
	return "qwdtt-" + strconv.FormatInt(time.Now().Unix(), 10)
}

// Save validates and stores the configuration into the qWDTT inbound, creating
// the inbound when there is none (the first save, and the migration from the
// old settings page). Enabled becomes the row's switch. The caller applies it.
func Save(cfg Config) (Config, error) {
	enabled := cfg.Enabled
	cfg, err := normalize(cfg)
	if err != nil {
		return cfg, err
	}
	cfg.Enabled = enabled
	storeMu.Lock()
	defer storeMu.Unlock()
	db := database.GetDB()
	in, err := LoadInbound(db)
	if err != nil {
		return cfg, err
	}
	if in == nil {
		in = &model.Inbound{
			Type:      InboundType,
			Tag:       uniqueTag(db),
			Enable:    enabled,
			EnableSet: true,
			Addrs:     json.RawMessage("null"),
			OutJson:   json.RawMessage("null"),
		}
		in.Options = cfg.options()
		return cfg, db.Create(in).Error
	}
	return cfg, db.Model(model.Inbound{}).Where("id = ?", in.Id).
		Updates(map[string]any{"options": []byte(cfg.options()), "enable": enabled}).Error
}

// update runs fn on the stored configuration under the store lock and saves
// the result back into the inbound's options (never its switch). fn returning
// an error aborts without saving. With no inbound there is nothing to update
// and fn is not run.
func update(fn func(*Config) error) (Config, error) {
	storeMu.Lock()
	defer storeMu.Unlock()
	db := database.GetDB()
	in, err := LoadInbound(db)
	if err != nil {
		return Config{}, err
	}
	if in == nil {
		return Default(), ErrNoInbound
	}
	cfg := decodeConfig(string(in.Options))
	cfg.Enabled = in.Enable
	if err := fn(&cfg); err != nil {
		return cfg, err
	}
	return cfg, db.Model(model.Inbound{}).Where("id = ?", in.Id).
		Update("options", []byte(cfg.options())).Error
}

// ErrNoInbound is returned when something needs the qWDTT inbound and none
// exists yet.
var ErrNoInbound = errors.New("qwdtt: there is no qWDTT inbound; add one on the Inbounds page")

// LegacyConfig returns the configuration the old standalone qWDTT page kept in
// qwdtt_states, plus who it gave the link to. ok is false when there is none.
// Only the one-time migration into an inbound reads it.
func LegacyConfig() (cfg Config, accessMode string, clients []string, ok bool, err error) {
	row, err := loadRow(database.GetDB())
	if err != nil {
		return Default(), "", nil, false, err
	}
	if strings.TrimSpace(row.Config) == "" {
		return Default(), "", nil, false, nil
	}
	cfg = decodeConfig(row.Config)
	var extra struct {
		Enabled    bool     `json:"enabled"`
		AccessMode string   `json:"accessMode"`
		Clients    []string `json:"clients"`
	}
	_ = json.Unmarshal([]byte(row.Config), &extra)
	cfg.Enabled = extra.Enabled
	return cfg, extra.AccessMode, extra.Clients, true, nil
}

// ClearLegacyConfig drops the old copy once an inbound has taken over, so the
// inbound is the only source and a later delete is not undone by a re-import.
// The VK cookies and the record of created calls stay.
func ClearLegacyConfig() error {
	storeMu.Lock()
	defer storeMu.Unlock()
	db := database.GetDB()
	row, err := loadRow(db)
	if err != nil {
		return err
	}
	row.Config = ""
	return saveRow(db, row)
}

// DetectOutboundIPv4 returns the IPv4 address the kernel would use for
// external traffic. It only connects a UDP socket; no packet is sent.
func DetectOutboundIPv4() string {
	d := net.Dialer{Timeout: 2 * time.Second}
	conn, err := d.DialContext(context.Background(), "udp4", "8.8.8.8:53")
	if err != nil {
		return ""
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || addr.IP == nil {
		return ""
	}
	ip := addr.IP.To4()
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
		return ""
	}
	return ip.String()
}
