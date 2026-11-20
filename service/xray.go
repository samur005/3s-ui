package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
	"github.com/samur005/3s-ui/util/common"
	"github.com/samur005/3s-ui/xray"

	"gorm.io/gorm"
)

func init() {
	registerExtDriver(&extDriver{
		Type:   xray.TypeVLESS,
		Engine: EngineXray,
		Normalize: func(tx *gorm.DB, id uint, options, stored json.RawMessage) (json.RawMessage, error) {
			raw, err := xray.NormalizeVless(id, options, stored)
			if err != nil {
				return nil, err
			}
			cfg, err := xray.DecodeVless(raw)
			if err != nil {
				return nil, err
			}
			if tag := strings.TrimSpace(cfg.OutboundTag); tag != "" {
				if err := xrayOutboundTagExists(tx, tag); err != nil {
					return nil, err
				}
			}
			return raw, nil
		},
		Apply: func() { (&XrayService{}).ApplyStored() },
	})
}

func xrayOutboundTagExists(tx *gorm.DB, tag string) error {
	var n int64
	if err := tx.Table("outbounds").Where("tag = ?", tag).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("outbound %q does not exist: pick one from Outbounds, or leave it empty for routing rules", tag)
	}
	return nil
}

// XrayService is the panel-facing API for the supervised Xray-core process.
type XrayService struct{}

// ApplyStored brings Xray (and its sing-box handoff) in line with the DB after
// an xvless save. It must not call RestartCore/StartCore synchronously: Save
// holds startMu.RLock and runs afterCommit under that lock, so a nested Lock
// deadlocks the panel (live on v2.4.3 after creating xvless).
//
// When the handoff fragment changed (or sing-box is down but required), queue
// the same async restart qWDTT/WG use. startLocked reapplies Xray only after
// the new mixed listeners exist. Otherwise rebuild Xray in place.
// Fail-closed: if a queued restart cannot raise handoff listeners, startLocked
// stops Xray rather than leaving it on freedom.
func (s *XrayService) ApplyStored() {
	var cs ConfigService
	db := database.GetDB()
	needCore := xray.AnyRouteThroughCore(db)
	if xray.CoreNeedsRestart() {
		if cs.CoreRunning() || needCore {
			cs.requestRestart()
			return
		}
	} else if needCore && !cs.CoreRunning() {
		cs.requestRestart()
		return
	}
	xray.Get().Apply(func() ([]byte, error) {
		return xray.BuildConfig(db)
	})
}

// Status returns the live state of the Xray process.
func (s *XrayService) Status() xray.Status {
	db := database.GetDB()
	var n int64
	_ = db.Table("inbounds").Where("type = ? AND node_id IS NULL AND enable = ?", xray.TypeVLESS, true).Count(&n)
	return xray.Get().Status(int(n))
}

// Restart rebuilds the config and restarts the process.
func (s *XrayService) Restart() xray.Status {
	s.ApplyStored()
	return s.Status()
}

// ClientLinks returns vless:// share links for every enabled xvless inbound
// the client is bound to.
func (s *XrayService) ClientLinks(clientID uint) ([]string, error) {
	db := database.GetDB()
	var client struct {
		Id       uint
		Name     string
		Enable   bool
		Config   json.RawMessage
		Inbounds json.RawMessage
	}
	if err := db.Table("clients").Select("id", "name", "enable", "config", "inbounds").
		Where("id = ?", clientID).Take(&client).Error; err != nil {
		return nil, common.NewError("client not found")
	}
	uuid, flow := "", ""
	{
		var m map[string]map[string]any
		_ = json.Unmarshal(client.Config, &m)
		for _, key := range []string{"xvless", "vless"} {
			if b, ok := m[key]; ok {
				if u, _ := b["uuid"].(string); u != "" {
					uuid = u
				}
				if f, _ := b["flow"].(string); f != "" {
					flow = f
				}
				if uuid != "" {
					break
				}
			}
		}
	}
	if uuid == "" {
		return nil, nil
	}
	var ids []uint
	_ = json.Unmarshal(client.Inbounds, &ids)
	want := map[uint]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var rows []struct {
		Id      uint
		Tag     string
		Enable  bool
		Options json.RawMessage
	}
	if err := db.Table("inbounds").Select("id", "tag", "enable", "options").
		Where("type = ? AND node_id IS NULL", xray.TypeVLESS).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]string, 0)
	for _, r := range rows {
		if !want[r.Id] || !r.Enable {
			continue
		}
		cfg, err := xray.DecodeVless(r.Options)
		if err != nil {
			continue
		}
		if flow != "" && cfg.FlowDefault == "" {
			cfg.FlowDefault = flow
		}
		link, err := xray.ShareLink(cfg, uuid, client.Name+" · "+r.Tag)
		if err != nil {
			continue
		}
		out = append(out, link)
	}
	return out, nil
}

// GenerateVlessEnc runs `xray vlessenc` and returns the ML-KEM-768 pair when
// the binary is available (falls back to explaining how to generate offline).
func (s *XrayService) GenerateVlessEnc() (map[string]string, error) {
	bin, err := xray.FindBinary()
	if err != nil {
		return nil, err
	}
	dec, enc, err := xray.GenerateVlessEnc(bin)
	if err != nil {
		return nil, err
	}
	return map[string]string{"decryption": dec, "encryption": enc}, nil
}

// LinksForClient is used by the subscription server.
func (s *XrayService) LinksForClient(client *model.Client) []string {
	if client == nil {
		return nil
	}
	links, err := s.ClientLinks(client.Id)
	if err != nil {
		return nil
	}
	return links
}
