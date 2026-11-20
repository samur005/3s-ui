package bisync

import (
	"encoding/json"
	"fmt"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
	"gorm.io/gorm"
)

// ApplyClientRecord applies a merge result for a client record.
func ApplyClientRecord(rec Record) error {
	db := database.GetDB()
	var existing model.Client
	err := db.Where("sync_uid = ?", rec.UID).First(&existing).Error
	notFound := err == gorm.ErrRecordNotFound
	if err != nil && !notFound {
		return err
	}
	if rec.DeletedAt > 0 {
		if notFound {
			// Insert tombstone stub so we remember the delete.
			stub := model.Client{
				Name: fmt.Sprintf("_tombstone_%s", rec.UID[:8]),
				Enable: false, SyncUID: rec.UID, SyncRev: rec.Rev, SyncBase: rec.Base,
				SyncOrigin: rec.Origin, DeletedAt: rec.DeletedAt,
				Inbounds: json.RawMessage("[]"), Config: json.RawMessage("{}"),
			}
			return db.Create(&stub).Error
		}
		return db.Model(&existing).Updates(map[string]any{
			"sync_rev": rec.Rev, "sync_base": rec.Base, "sync_origin": rec.Origin,
			"deleted_at": rec.DeletedAt, "enable": false,
		}).Error
	}
	name, _ := rec.Payload["name"].(string)
	if name == "" {
		return fmt.Errorf("client payload missing name")
	}
	enable, _ := rec.Payload["enable"].(bool)
	desc, _ := rec.Payload["desc"].(string)
	expiry := int64From(rec.Payload["expiry"])
	limitIp := intFrom(rec.Payload["limitIp"])
	cfg, _ := json.Marshal(rec.Payload["config"])
	inbIDs := resolveInboundIDs(rec.Payload["inboundsSync"])
	inbJSON, _ := json.Marshal(inbIDs)

	if notFound {
		// Avoid name collision with a local non-synced client.
		var clash model.Client
		if db.Where("name = ? AND deleted_at = 0", name).First(&clash).Error == nil {
			return fmt.Errorf("duplicate client name %q (local id %d) — resolve before sync", name, clash.Id)
		}
		c := model.Client{
			Name: name, Enable: enable, Desc: desc, Expiry: expiry, LimitIp: limitIp,
			Config: cfg, Inbounds: inbJSON,
			SyncUID: rec.UID, SyncRev: rec.Rev, SyncBase: rec.Base, SyncOrigin: rec.Origin,
			Group: "", // not @cluster — peer-owned enrolled client
		}
		return db.Create(&c).Error
	}
	return db.Model(&existing).Updates(map[string]any{
		"name": name, "enable": enable, "desc": desc, "expiry": expiry, "limit_ip": limitIp,
		"config": cfg, "inbounds": inbJSON,
		"sync_rev": rec.Rev, "sync_base": rec.Base, "sync_origin": rec.Origin, "deleted_at": 0,
	}).Error
}

// ApplyInboundRecord applies a template inbound; local listen stays untouched on update.
func ApplyInboundRecord(rec Record) error {
	db := database.GetDB()
	var existing model.Inbound
	err := db.Where("sync_uid = ?", rec.UID).First(&existing).Error
	notFound := err == gorm.ErrRecordNotFound
	if err != nil && !notFound {
		return err
	}
	if rec.DeletedAt > 0 {
		if notFound {
			return nil
		}
		return db.Model(&existing).Updates(map[string]any{
			"sync_rev": rec.Rev, "sync_base": rec.Base, "sync_origin": rec.Origin,
			"deleted_at": rec.DeletedAt, "enable": false,
		}).Error
	}
	typ, _ := rec.Payload["type"].(string)
	tag, _ := rec.Payload["tag"].(string)
	enable, _ := rec.Payload["enable"].(bool)
	opts, _ := json.Marshal(rec.Payload["options"])
	oj, _ := json.Marshal(rec.Payload["out_json"])
	if typ == "" || tag == "" {
		return fmt.Errorf("inbound payload missing type/tag")
	}
	if notFound {
		var clash model.Inbound
		if db.Where("tag = ? AND deleted_at = 0", tag).First(&clash).Error == nil {
			return fmt.Errorf("duplicate inbound tag %q — resolve before sync", tag)
		}
		// Safe local stubs — operator must fill listen_port (needsLocal).
		if opts == nil || string(opts) == "null" {
			opts = []byte(`{"listen":"0.0.0.0","listen_port":0}`)
		} else {
			var m map[string]any
			_ = json.Unmarshal(opts, &m)
			if m == nil {
				m = map[string]any{}
			}
			if _, ok := m["listen"]; !ok {
				m["listen"] = "0.0.0.0"
			}
			if _, ok := m["listen_port"]; !ok {
				m["listen_port"] = 0
			}
			opts, _ = json.Marshal(m)
		}
		ib := model.Inbound{
			Type: typ, Tag: tag, Enable: enable, Options: opts, OutJson: oj,
			Addrs: json.RawMessage("[]"),
			SyncUID: rec.UID, SyncRev: rec.Rev, SyncBase: rec.Base, SyncOrigin: rec.Origin,
			SyncNeedsLocal: true,
		}
		return db.Create(&ib).Error
	}
	// Update template fields only; keep listen/tls/addrs.
	updates := map[string]any{
		"type": typ, "tag": tag, "enable": enable,
		"sync_rev": rec.Rev, "sync_base": rec.Base, "sync_origin": rec.Origin, "deleted_at": 0,
	}
	// Merge options: keep local listen* keys.
	var curOpts, newOpts map[string]any
	_ = json.Unmarshal(existing.Options, &curOpts)
	_ = json.Unmarshal(opts, &newOpts)
	if curOpts == nil {
		curOpts = map[string]any{}
	}
	for k, v := range newOpts {
		if inboundLocalOptionKeys[k] {
			continue
		}
		curOpts[k] = v
	}
	mergedOpts, _ := json.Marshal(curOpts)
	updates["options"] = mergedOpts
	var curOJ, newOJ map[string]any
	_ = json.Unmarshal(existing.OutJson, &curOJ)
	_ = json.Unmarshal(oj, &newOJ)
	if curOJ == nil {
		curOJ = map[string]any{}
	}
	for k, v := range newOJ {
		if inboundLocalOutKeys[k] {
			continue
		}
		curOJ[k] = v
	}
	mergedOJ, _ := json.Marshal(curOJ)
	updates["out_json"] = mergedOJ
	return db.Model(&existing).Updates(updates).Error
}

func resolveInboundIDs(raw any) []uint {
	arr, ok := raw.([]any)
	if !ok {
		if ss, ok2 := raw.([]string); ok2 {
			arr = make([]any, len(ss))
			for i, s := range ss {
				arr[i] = s
			}
		} else {
			return nil
		}
	}
	db := database.GetDB()
	var ids []uint
	for _, v := range arr {
		uid, _ := v.(string)
		if uid == "" {
			continue
		}
		var ib model.Inbound
		if err := db.Select("id").Where("sync_uid = ? AND deleted_at = 0", uid).First(&ib).Error; err != nil {
			continue
		}
		ids = append(ids, ib.Id)
	}
	return ids
}

func int64From(v any) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case int64:
		return t
	case int:
		return int64(t)
	default:
		return 0
	}
}

func intFrom(v any) int { return int(int64From(v)) }

// StoreConflict persists an unresolved conflict.
func StoreConflict(nodeID uint, c *Conflict) error {
	if c == nil {
		return nil
	}
	db := database.GetDB()
	lj, _ := json.Marshal(c.Local)
	rj, _ := json.Marshal(c.Remote)
	row := model.SyncConflict{
		UID: c.UID, Kind: c.Kind, NodeId: nodeID,
		LocalJSON: string(lj), RemoteJSON: string(rj),
		CreatedAt: c.CreatedAt, Resolved: false,
	}
	// Upsert by uid+kind unresolved
	var existing model.SyncConflict
	err := db.Where("uid = ? AND kind = ? AND resolved = ?", c.UID, c.Kind, false).First(&existing).Error
	if err == nil {
		return db.Model(&existing).Updates(map[string]any{
			"local_json": row.LocalJSON, "remote_json": row.RemoteJSON,
			"created_at": row.CreatedAt, "node_id": nodeID,
		}).Error
	}
	return db.Create(&row).Error
}

// ListConflicts returns unresolved conflicts.
func ListConflicts() ([]model.SyncConflict, error) {
	var rows []model.SyncConflict
	err := database.GetDB().Where("resolved = ?", false).Order("created_at desc").Find(&rows).Error
	return rows, err
}

// ResolveConflict applies operator decision and marks resolved.
func ResolveConflict(id uint, decision Decision) error {
	pid, err := PanelSyncID()
	if err != nil {
		return err
	}
	db := database.GetDB()
	var row model.SyncConflict
	if err := db.First(&row, id).Error; err != nil {
		return err
	}
	if row.Resolved {
		return nil
	}
	var local, remote Record
	_ = json.Unmarshal([]byte(row.LocalJSON), &local)
	_ = json.Unmarshal([]byte(row.RemoteJSON), &remote)
	res, ok := Resolve(Conflict{UID: row.UID, Kind: row.Kind, Local: local, Remote: remote}, decision, pid)
	if !ok {
		return nil
	}
	switch row.Kind {
	case "client":
		if err := ApplyClientRecord(res.Record); err != nil {
			return err
		}
	case "inbound":
		if err := ApplyInboundRecord(res.Record); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown kind %s", row.Kind)
	}
	return db.Model(&row).Update("resolved", true).Error
}

func lookupClient(uid string) (Record, bool) {
	var c model.Client
	if database.GetDB().Where("sync_uid = ?", uid).First(&c).Error != nil {
		return Record{}, false
	}
	return Record{UID: c.SyncUID, Kind: "client", Rev: c.SyncRev, Base: c.SyncBase, Origin: c.SyncOrigin, DeletedAt: c.DeletedAt}, true
}

func lookupInbound(uid string) (Record, bool) {
	var ib model.Inbound
	if database.GetDB().Where("sync_uid = ?", uid).First(&ib).Error != nil {
		return Record{}, false
	}
	return Record{UID: ib.SyncUID, Kind: "inbound", Rev: ib.SyncRev, Base: ib.SyncBase, Origin: ib.SyncOrigin, DeletedAt: ib.DeletedAt}, true
}
