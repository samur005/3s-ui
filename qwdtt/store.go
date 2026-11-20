package qwdtt

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// storeMu serializes read-modify-write cycles on the single state row; the
// panel's API handlers and the cron job both write it.
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

// Load returns the stored configuration merged over the defaults. A fresh
// install returns Default() (disabled, no password yet).
func Load() (Config, error) {
	row, err := loadRow(database.GetDB())
	if err != nil {
		return Config{}, err
	}
	return decodeConfig(row.Config), nil
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

// Save validates and stores the configuration. A missing password is
// generated. The caller applies it (Manager.Apply).
func Save(cfg Config) (Config, error) {
	cfg = cfg.Merge()
	cfg.Clients = cleanNames(cfg.Clients)
	cfg.VkHashes = NormalizeHashes(cfg.VkHashes)
	cfg.SubHost = strings.TrimSpace(cfg.SubHost)
	cfg.OutboundTag = strings.TrimSpace(cfg.OutboundTag)
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	cfg, err := cfg.EnsurePassword()
	if err != nil {
		return cfg, err
	}
	storeMu.Lock()
	defer storeMu.Unlock()
	db := database.GetDB()
	row, err := loadRow(db)
	if err != nil {
		return cfg, err
	}
	row.Config = cfg.encode()
	return cfg, saveRow(db, row)
}

// update runs fn on the stored configuration under the store lock and saves
// the result. fn returning an error aborts without saving.
func update(fn func(*Config) error) (Config, error) {
	storeMu.Lock()
	defer storeMu.Unlock()
	db := database.GetDB()
	row, err := loadRow(db)
	if err != nil {
		return Config{}, err
	}
	cfg := decodeConfig(row.Config)
	if err := fn(&cfg); err != nil {
		return cfg, err
	}
	row.Config = cfg.encode()
	return cfg, saveRow(db, row)
}

func cleanNames(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, n := range in {
		n = strings.TrimSpace(n)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
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
