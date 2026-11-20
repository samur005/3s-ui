package bisync

import (
	"encoding/json"

	"github.com/google/uuid"
	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
)

// Catalog is the wire format exchanged between panels.
type Catalog struct {
	PanelID   string   `json:"panelId"`
	Proto     int      `json:"proto"`
	Clients   []Record `json:"clients"`
	Inbounds  []Record `json:"inbounds"`
}

// BuildCatalog exports enrolled (sync_uid != "") entities from this panel.
func BuildCatalog() (*Catalog, error) {
	pid, err := PanelSyncID()
	if err != nil {
		return nil, err
	}
	db := database.GetDB()
	var clients []model.Client
	if err := db.Where("sync_uid <> ''").Find(&clients).Error; err != nil {
		return nil, err
	}
	var inbounds []model.Inbound
	if err := db.Where("sync_uid <> '' AND node_id IS NULL").Find(&inbounds).Error; err != nil {
		return nil, err
	}
	cat := &Catalog{PanelID: pid, Proto: ProtocolVersion}
	for i := range clients {
		c := &clients[i]
		rec := Record{
			UID: c.SyncUID, Kind: "client", Rev: c.SyncRev, Base: c.SyncBase,
			Origin: c.SyncOrigin, DeletedAt: c.DeletedAt,
		}
		if c.DeletedAt == 0 {
			var inbIDs []any
			_ = json.Unmarshal(c.Inbounds, &inbIDs)
			// Map local inbound ids → sync_uids for peer binding.
			syncTags := inboundSyncUIDs(inbIDs)
			rec.Payload = ClientPayload(map[string]any{
				"name": c.Name, "enable": c.Enable, "config": jsonRaw(c.Config),
				"expiry": c.Expiry, "desc": c.Desc, "limitIp": c.LimitIp,
				"inboundsSync": syncTags,
			})
		}
		cat.Clients = append(cat.Clients, rec)
	}
	for i := range inbounds {
		ib := &inbounds[i]
		rec := Record{
			UID: ib.SyncUID, Kind: "inbound", Rev: ib.SyncRev, Base: ib.SyncBase,
			Origin: ib.SyncOrigin, DeletedAt: ib.DeletedAt, NeedsLocal: ib.SyncNeedsLocal,
		}
		if ib.DeletedAt == 0 {
			var opts map[string]any
			_ = json.Unmarshal(ib.Options, &opts)
			var oj map[string]any
			_ = json.Unmarshal(ib.OutJson, &oj)
			rec.Payload = InboundTemplatePayload(map[string]any{
				"type": ib.Type, "tag": ib.Tag, "enable": ib.Enable,
				"options": opts, "out_json": oj,
			})
			if UnsupportedInboundClone(ib.Type) {
				rec.Payload["cloneLimited"] = true
			}
		}
		cat.Inbounds = append(cat.Inbounds, rec)
	}
	return cat, nil
}

func jsonRaw(r json.RawMessage) any {
	if len(r) == 0 {
		return nil
	}
	var v any
	if json.Unmarshal(r, &v) != nil {
		return string(r)
	}
	return v
}

func inboundSyncUIDs(ids []any) []string {
	if len(ids) == 0 {
		return nil
	}
	db := database.GetDB()
	var out []string
	for _, raw := range ids {
		var id uint
		switch t := raw.(type) {
		case float64:
			id = uint(t)
		case int:
			id = uint(t)
		case json.Number:
			n, _ := t.Int64()
			id = uint(n)
		default:
			continue
		}
		var ib model.Inbound
		if err := db.Select("sync_uid").Where("id = ?", id).First(&ib).Error; err != nil {
			continue
		}
		if ib.SyncUID != "" {
			out = append(out, ib.SyncUID)
		}
	}
	return out
}

// EnrollClient assigns a sync_uid if missing and bumps rev for first publish.
func EnrollClient(id uint) error {
	pid, err := PanelSyncID()
	if err != nil {
		return err
	}
	db := database.GetDB()
	var c model.Client
	if err := db.First(&c, id).Error; err != nil {
		return err
	}
	if c.SyncUID == "" {
		c.SyncUID = uuid.NewString()
		c.SyncRev = 1
		c.SyncBase = 0
		c.SyncOrigin = pid
	} else {
		n := BumpRev(Record{UID: c.SyncUID, Rev: c.SyncRev, Base: c.SyncBase, Origin: c.SyncOrigin}, pid)
		c.SyncRev, c.SyncBase, c.SyncOrigin = n.Rev, n.Base, n.Origin
	}
	return db.Model(&c).Updates(map[string]any{
		"sync_uid": c.SyncUID, "sync_rev": c.SyncRev, "sync_base": c.SyncBase, "sync_origin": c.SyncOrigin,
	}).Error
}

// EnrollInbound assigns sync identity to a local inbound.
func EnrollInbound(id uint) error {
	pid, err := PanelSyncID()
	if err != nil {
		return err
	}
	db := database.GetDB()
	var ib model.Inbound
	if err := db.First(&ib, id).Error; err != nil {
		return err
	}
	if ib.NodeId != nil {
		return errNodeReplica
	}
	if ib.SyncUID == "" {
		ib.SyncUID = uuid.NewString()
		ib.SyncRev = 1
		ib.SyncBase = 0
		ib.SyncOrigin = pid
	} else {
		n := BumpRev(Record{UID: ib.SyncUID, Rev: ib.SyncRev, Base: ib.SyncBase, Origin: ib.SyncOrigin}, pid)
		ib.SyncRev, ib.SyncBase, ib.SyncOrigin = n.Rev, n.Base, n.Origin
	}
	return db.Model(&ib).Updates(map[string]any{
		"sync_uid": ib.SyncUID, "sync_rev": ib.SyncRev, "sync_base": ib.SyncBase, "sync_origin": ib.SyncOrigin,
	}).Error
}

var errNodeReplica = errString("cannot enroll a node replica inbound")

type errString string

func (e errString) Error() string { return string(e) }

// UnenrollClient removes a client from bidirectional sync (clears sync identity).
func UnenrollClient(id uint) error {
	db := database.GetDB()
	return db.Model(&model.Client{}).Where("id = ?", id).Updates(map[string]any{
		"sync_uid": "", "sync_rev": 0, "sync_base": 0, "sync_origin": "", "deleted_at": 0,
	}).Error
}

// UnenrollInbound removes a local inbound from bidirectional sync.
func UnenrollInbound(id uint) error {
	db := database.GetDB()
	var ib model.Inbound
	if err := db.First(&ib, id).Error; err != nil {
		return err
	}
	if ib.NodeId != nil {
		return errNodeReplica
	}
	return db.Model(&ib).Updates(map[string]any{
		"sync_uid": "", "sync_rev": 0, "sync_base": 0, "sync_origin": "", "deleted_at": 0, "sync_needs_local": false,
	}).Error
}
