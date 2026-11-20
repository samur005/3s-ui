package bisync

import (
	"path/filepath"
	"testing"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
)

// Simulates master→node→master exchange without HTTP by swapping InitDB files
// is awkward; instead we enroll on A, BuildCatalog, Ingest on B, edit on B,
// ingest back to A and expect conflict when both edited.
func TestTwoPanelClientRoundTripAndConflict(t *testing.T) {
	ResetPanelSyncIDForTest()
	dir := t.TempDir()
	if err := database.InitDB(filepath.Join(dir, "panelA.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.CloseDBForTest(); ResetPanelSyncIDForTest() })

	// Seed panel A client and enroll
	c := model.Client{Name: "shared", Enable: true, Config: []byte(`{}`), Inbounds: []byte(`[]`)}
	if err := database.GetDB().Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	if err := EnrollClient(c.Id); err != nil {
		t.Fatal(err)
	}
	catA, err := BuildCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(catA.Clients) != 1 {
		t.Fatalf("catalog A clients=%d", len(catA.Clients))
	}
	uid := catA.Clients[0].UID
	pidA := catA.PanelID

	// Close A, open B, ingest
	_ = database.CloseDBForTest()
	ResetPanelSyncIDForTest()
	if err := database.InitDB(filepath.Join(dir, "panelB.db")); err != nil {
		t.Fatal(err)
	}
	if err := IngestRemoteCatalog(0, catA); err != nil {
		t.Fatal(err)
	}
	var b model.Client
	if err := database.GetDB().Where("sync_uid = ?", uid).First(&b).Error; err != nil {
		t.Fatal(err)
	}
	if b.Name != "shared" {
		t.Fatalf("B name %s", b.Name)
	}
	pidB, _ := PanelSyncID()
	if pidB == pidA {
		t.Fatal("panels must have distinct sync ids")
	}
	// Edit on B
	n := BumpRev(Record{UID: uid, Rev: b.SyncRev, Base: b.SyncBase, Origin: b.SyncOrigin}, pidB)
	if err := database.GetDB().Model(&b).Updates(map[string]any{
		"name": "shared-b", "sync_rev": n.Rev, "sync_base": n.Base, "sync_origin": n.Origin,
	}).Error; err != nil {
		t.Fatal(err)
	}
	catB, err := BuildCatalog()
	if err != nil {
		t.Fatal(err)
	}

	// Also edit on A (concurrent) then ingest B → conflict
	_ = database.CloseDBForTest()
	ResetPanelSyncIDForTest()
	if err := database.InitDB(filepath.Join(dir, "panelA.db")); err != nil {
		t.Fatal(err)
	}
	var a model.Client
	if err := database.GetDB().Where("sync_uid = ?", uid).First(&a).Error; err != nil {
		t.Fatal(err)
	}
	na := BumpRev(Record{UID: uid, Rev: a.SyncRev, Base: a.SyncBase, Origin: a.SyncOrigin}, pidA)
	if err := database.GetDB().Model(&a).Updates(map[string]any{
		"name": "shared-a", "sync_rev": na.Rev, "sync_base": na.Base, "sync_origin": na.Origin,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := IngestRemoteCatalog(7, catB); err != nil {
		t.Fatal(err)
	}
	rows, err := ListConflicts()
	if err != nil || len(rows) == 0 {
		t.Fatalf("expected conflict, got %v err=%v", rows, err)
	}
	// Name should still be shared-a (conflict does not auto-apply remote)
	if err := database.GetDB().Where("sync_uid = ?", uid).First(&a).Error; err != nil {
		t.Fatal(err)
	}
	if a.Name != "shared-a" {
		t.Fatalf("LWW happened? name=%s", a.Name)
	}
}
