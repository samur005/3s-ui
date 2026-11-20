package bisync

import (
	"path/filepath"
	"testing"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
)

func TestOfflineReplayAndReorder(t *testing.T) {
	ResetPanelSyncIDForTest()
	dir := t.TempDir()

	// Panel A: create and enroll client
	if err := database.InitDB(filepath.Join(dir, "a.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.CloseDBForTest(); ResetPanelSyncIDForTest() })

	c := model.Client{Name: "replay", Enable: true, Config: []byte(`{}`), Inbounds: []byte(`[]`)}
	if err := database.GetDB().Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	if err := EnrollClient(c.Id); err != nil {
		t.Fatal(err)
	}
	cat1, err := BuildCatalog()
	if err != nil {
		t.Fatal(err)
	}
	uid := cat1.Clients[0].UID
	pidA := cat1.PanelID

	// Simulate offline: close A, work on B with old catalog, then later catalogs
	_ = database.CloseDBForTest()
	ResetPanelSyncIDForTest()
	if err := database.InitDB(filepath.Join(dir, "b.db")); err != nil {
		t.Fatal(err)
	}
	if err := IngestRemoteCatalog(0, cat1); err != nil {
		t.Fatal(err)
	}

	// B edits while "offline" from A
	var b model.Client
	if err := database.GetDB().Where("sync_uid = ?", uid).First(&b).Error; err != nil {
		t.Fatal(err)
	}
	pidB, _ := PanelSyncID()
	n := BumpRev(Record{UID: uid, Rev: b.SyncRev, Base: b.SyncBase, Origin: b.SyncOrigin}, pidB)
	if err := database.GetDB().Model(&b).Updates(map[string]any{
		"name": "replay-b", "desc": "from-b",
		"sync_rev": n.Rev, "sync_base": n.Base, "sync_origin": n.Origin,
	}).Error; err != nil {
		t.Fatal(err)
	}
	catB, err := BuildCatalog()
	if err != nil {
		t.Fatal(err)
	}

	// Now A comes online and also edited in the meantime (reorder scenario)
	_ = database.CloseDBForTest()
	ResetPanelSyncIDForTest()
	if err := database.InitDB(filepath.Join(dir, "a.db")); err != nil {
		t.Fatal(err)
	}
	var a model.Client
	if err := database.GetDB().Where("sync_uid = ?", uid).First(&a).Error; err != nil {
		t.Fatal(err)
	}
	na := BumpRev(Record{UID: uid, Rev: a.SyncRev, Base: a.SyncBase, Origin: a.SyncOrigin}, pidA)
	if err := database.GetDB().Model(&a).Updates(map[string]any{
		"name": "replay-a", "desc": "from-a",
		"sync_rev": na.Rev, "sync_base": na.Base, "sync_origin": na.Origin,
	}).Error; err != nil {
		t.Fatal(err)
	}

	// Ingest B's catalog (should conflict)
	if err := IngestRemoteCatalog(1, catB); err != nil {
		t.Fatal(err)
	}
	rows, err := ListConflicts()
	if err != nil || len(rows) != 1 {
		t.Fatalf("expected 1 conflict after offline replay, got %d err=%v", len(rows), err)
	}

	// Resolve keep local and verify
	if err := ResolveConflict(rows[0].Id, KeepLocal); err != nil {
		t.Fatal(err)
	}
	if err := database.GetDB().Where("sync_uid = ?", uid).First(&a).Error; err != nil {
		t.Fatal(err)
	}
	if a.Name != "replay-a" {
		t.Fatalf("after keep-local name=%s", a.Name)
	}
}

func TestTombstoneListAndPurge(t *testing.T) {
	ResetPanelSyncIDForTest()
	dir := t.TempDir()
	if err := database.InitDB(filepath.Join(dir, "t.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.CloseDBForTest(); ResetPanelSyncIDForTest() })

	c := model.Client{Name: "to-delete", Enable: true, Config: []byte(`{}`), Inbounds: []byte(`[]`)}
	if err := database.GetDB().Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	if err := EnrollClient(c.Id); err != nil {
		t.Fatal(err)
	}
	var loaded model.Client
	if err := database.GetDB().First(&loaded, c.Id).Error; err != nil {
		t.Fatal(err)
	}
	pid, _ := PanelSyncID()
	tomb := Tombstone(Record{UID: loaded.SyncUID, Rev: loaded.SyncRev, Base: loaded.SyncBase, Origin: loaded.SyncOrigin}, pid)
	if err := database.GetDB().Model(&loaded).Updates(map[string]any{
		"deleted_at": tomb.DeletedAt, "enable": false,
		"sync_rev": tomb.Rev, "sync_base": tomb.Base, "sync_origin": tomb.Origin,
	}).Error; err != nil {
		t.Fatal(err)
	}

	items, err := ListTombstones()
	if err != nil || len(items) != 1 || items[0].Name != "to-delete" {
		t.Fatalf("list tombstones: %+v err=%v", items, err)
	}
	if err := PurgeTombstone("client", items[0].ID); err != nil {
		t.Fatal(err)
	}
	items, err = ListTombstones()
	if err != nil || len(items) != 0 {
		t.Fatalf("after purge: %+v", items)
	}
}
