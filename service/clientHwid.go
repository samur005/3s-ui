package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"

	"gorm.io/gorm"
)

// Per-client device limit for subscriptions (Client.LimitHwid), keyed on the
// X-HWID header that Happ, v2RayTun, Hiddify and others send with every
// subscription fetch. Same behaviour as LucX's HWID gate, implemented
// independently: the raw id is never stored, only its SHA-256.

const minHwidLength = 6

type HwidRequest struct {
	Hwid, UserAgent, DeviceOS, OsVersion, DeviceModel string
}

// HwidGateResult drives the X-Hwid-* response headers and the 404.
type HwidGateResult struct {
	Allowed           bool
	Active            bool // the client has a device limit
	NotSupported      bool // limit set, but the app sent no usable X-HWID
	LimitReached      bool // all slots used (this device is one of them)
	MaxDevicesReached bool // a new device was refused
	Limit             int
	Registered        int
}

func hashHwid(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func trimMeta(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// EnforceHwid registers the device or refuses it. A client without a limit,
// and a request without a usable X-HWID, are allowed (the latter flagged
// NotSupported, as apps that never send one must keep working).
func (s *ClientService) EnforceHwid(client *model.Client, req HwidRequest) (HwidGateResult, error) {
	res := HwidGateResult{Allowed: true}
	if client == nil || client.LimitHwid <= 0 {
		return res, nil
	}
	res.Active = true
	res.Limit = client.LimitHwid
	hwid := strings.TrimSpace(req.Hwid)
	if len(hwid) < minHwidLength {
		res.NotSupported = true
		return res, nil
	}
	h := hashHwid(hwid)
	now := time.Now().Unix()
	meta := map[string]any{
		"last_seen": now, "user_agent": trimMeta(req.UserAgent, 512), "device_os": trimMeta(req.DeviceOS, 128),
		"os_version": trimMeta(req.OsVersion, 128), "device_model": trimMeta(req.DeviceModel, 128),
	}
	err := database.GetDB().Transaction(func(tx *gorm.DB) error {
		var existing model.ClientHwid
		err := tx.Where("client_id = ? AND hwid_hash = ?", client.Id, h).First(&existing).Error
		var count int64
		if err == nil {
			if err := tx.Model(model.ClientHwid{}).Where("id = ?", existing.Id).Updates(meta).Error; err != nil {
				return err
			}
			if err := tx.Model(model.ClientHwid{}).Where("client_id = ?", client.Id).Count(&count).Error; err != nil {
				return err
			}
			res.Registered = int(count)
			res.LimitReached = int(count) >= client.LimitHwid
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Model(model.ClientHwid{}).Where("client_id = ?", client.Id).Count(&count).Error; err != nil {
			return err
		}
		res.Registered = int(count)
		if int(count) >= client.LimitHwid {
			res.Allowed = false
			res.LimitReached = true
			res.MaxDevicesReached = true
			return nil
		}
		row := model.ClientHwid{
			ClientId: client.Id, HwidHash: h, FirstSeen: now, LastSeen: now,
			UserAgent: meta["user_agent"].(string), DeviceOS: meta["device_os"].(string),
			OsVersion: meta["os_version"].(string), DeviceModel: meta["device_model"].(string),
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		res.Registered = int(count) + 1
		res.LimitReached = res.Registered >= client.LimitHwid
		return nil
	})
	if err != nil {
		return HwidGateResult{}, err
	}
	return res, nil
}

// ListHwids lists a client's registered devices (hash shortened for display).
func (s *ClientService) ListHwids(clientId uint) ([]model.ClientHwid, error) {
	var rows []model.ClientHwid
	err := database.GetDB().Where("client_id = ?", clientId).Order("last_seen desc").Find(&rows).Error
	for i := range rows {
		if len(rows[i].HwidHash) > 12 {
			rows[i].HwidHash = rows[i].HwidHash[:12]
		}
	}
	return rows, err
}

// DeleteHwids frees one device slot (id > 0) or all of a client's (id == 0).
func (s *ClientService) DeleteHwids(clientId uint, id uint) error {
	q := database.GetDB().Where("client_id = ?", clientId)
	if id > 0 {
		q = q.Where("id = ?", id)
	}
	return q.Delete(&model.ClientHwid{}).Error
}
