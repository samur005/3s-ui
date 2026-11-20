package bisync

import (
	"time"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
)

// TombstoneItem is a view of a soft-deleted enrolled record awaiting confirmation.
type TombstoneItem struct {
	Kind      string `json:"kind"`
	ID        uint   `json:"id"`
	UID       string `json:"uid"`
	Name      string `json:"name"`
	DeletedAt int64  `json:"deletedAt"`
}

// ListTombstones returns enrolled clients and inbounds that have a tombstone.
func ListTombstones() ([]TombstoneItem, error) {
	db := database.GetDB()
	var out []TombstoneItem

	var clients []model.Client
	if err := db.Where("sync_uid <> '' AND deleted_at > 0").Find(&clients).Error; err != nil {
		return nil, err
	}
	for _, c := range clients {
		out = append(out, TombstoneItem{
			Kind: "client", ID: c.Id, UID: c.SyncUID, Name: c.Name, DeletedAt: c.DeletedAt,
		})
	}

	var inbounds []model.Inbound
	if err := db.Where("sync_uid <> '' AND deleted_at > 0 AND node_id IS NULL").Find(&inbounds).Error; err != nil {
		return nil, err
	}
	for _, ib := range inbounds {
		out = append(out, TombstoneItem{
			Kind: "inbound", ID: ib.Id, UID: ib.SyncUID, Name: ib.Tag, DeletedAt: ib.DeletedAt,
		})
	}
	return out, nil
}

// PurgeTombstone permanently deletes a tombstoned record after operator confirmation.
func PurgeTombstone(kind string, id uint) error {
	db := database.GetDB()
	switch kind {
	case "client":
		return db.Where("id = ? AND deleted_at > 0 AND sync_uid <> ''", id).Delete(&model.Client{}).Error
	case "inbound":
		return db.Where("id = ? AND deleted_at > 0 AND sync_uid <> '' AND node_id IS NULL", id).Delete(&model.Inbound{}).Error
	default:
		return errString("kind must be client or inbound")
	}
}

// PurgeExpiredTombstones removes tombstones older than the given duration (both sides should have acked by then).
func PurgeExpiredTombstones(ttl time.Duration) (int, error) {
	cutoff := time.Now().Add(-ttl).Unix()
	db := database.GetDB()
	res1 := db.Where("sync_uid <> '' AND deleted_at > 0 AND deleted_at < ?", cutoff).Delete(&model.Client{})
	res2 := db.Where("sync_uid <> '' AND deleted_at > 0 AND deleted_at < ? AND node_id IS NULL", cutoff).Delete(&model.Inbound{})
	return int(res1.RowsAffected + res2.RowsAffected), nil
}
