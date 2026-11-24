package service

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/samur005/3s-ui/core"
	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
	"github.com/samur005/3s-ui/logger"
	"github.com/samur005/3s-ui/service/bisync"

	"github.com/op/go-logging"
)

// Deleting a client on a panel whose sync id was never minted used to mint it
// from inside the delete's write transaction through a second connection,
// which waited on that transaction until busy_timeout: "database is locked"
// after 10 s, every first client delete on a fresh panel.
func TestClientDeleteOnFreshPanelDoesNotLock(t *testing.T) {
	logger.InitLogger(logging.CRITICAL)
	if err := database.InitDB(filepath.Join(t.TempDir(), "fresh.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.CloseDBForTest() })
	bisync.ResetPanelSyncIDForTest()
	t.Cleanup(bisync.ResetPanelSyncIDForTest)
	previous := corePtr
	corePtr = core.NewCore()
	t.Cleanup(func() { corePtr = previous })

	c := model.Client{Enable: true, Name: "u1", Config: json.RawMessage(`{}`), Inbounds: json.RawMessage(`[]`), Links: json.RawMessage(`[]`)}
	if err := database.GetDB().Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := (&ConfigService{}).Save("clients", "del", json.RawMessage(strconv.Itoa(int(c.Id))), "", "admin", ""); err != nil {
		t.Fatalf("delete: %v (after %s)", err, time.Since(start))
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("delete took %s", d)
	}
	var n int64
	database.GetDB().Model(model.Client{}).Where("id = ?", c.Id).Count(&n)
	if n != 0 {
		t.Error("client still there")
	}
}
