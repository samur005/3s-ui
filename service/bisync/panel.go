package bisync

import (
	"crypto/rand"
	"encoding/hex"
	"sync"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
)

const panelSyncIDKey = "panelSyncId"

var (
	panelIDMu sync.Mutex
	panelID   string
)

// PanelSyncID returns this panel's stable origin id, creating one if missing.
func PanelSyncID() (string, error) {
	panelIDMu.Lock()
	defer panelIDMu.Unlock()
	if panelID != "" {
		return panelID, nil
	}
	db := database.GetDB()
	var row model.Setting
	err := db.Where("`key` = ?", panelSyncIDKey).First(&row).Error
	if err == nil && row.Value != "" {
		panelID = row.Value
		return panelID, nil
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := "p-" + hex.EncodeToString(b)
	if err := db.Create(&model.Setting{Key: panelSyncIDKey, Value: id}).Error; err != nil {
		// Race: another process created it — re-read.
		if err2 := db.Where("`key` = ?", panelSyncIDKey).First(&row).Error; err2 != nil {
			return "", err
		}
		panelID = row.Value
		return panelID, nil
	}
	panelID = id
	return panelID, nil
}

// ResetPanelSyncIDForTest clears the cache (tests only).
func ResetPanelSyncIDForTest() {
	panelIDMu.Lock()
	panelID = ""
	panelIDMu.Unlock()
}
