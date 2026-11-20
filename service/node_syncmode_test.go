package service

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
)

func TestNodeSyncModeDefaultOneWay(t *testing.T) {
	if err := database.InitDB(filepath.Join(t.TempDir(), "syncmode.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.CloseDBForTest() })

	svc := NodeService{}
	payload, _ := json.Marshal(map[string]any{
		"enable": true, "name": "n1", "baseUrl": "https://127.0.0.1:2095",
		"webPath": "/app/", "token": "tok",
	})
	if err := svc.Save(database.GetDB(), "new", payload); err != nil {
		t.Fatal(err)
	}
	var n model.Node
	if err := database.GetDB().Where("name = ?", "n1").First(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n.SyncMode != "one_way" && n.SyncMode != "" {
		t.Fatalf("default syncMode=%q", n.SyncMode)
	}
}

func TestNodeSyncModeBidirectionalOptIn(t *testing.T) {
	if err := database.InitDB(filepath.Join(t.TempDir(), "syncmode2.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.CloseDBForTest() })

	svc := NodeService{}
	payload, _ := json.Marshal(map[string]any{
		"enable": true, "name": "n2", "baseUrl": "https://127.0.0.1:2095",
		"webPath": "/app/", "token": "tok", "syncMode": "bidirectional",
	})
	if err := svc.Save(database.GetDB(), "new", payload); err != nil {
		t.Fatal(err)
	}
	var n model.Node
	if err := database.GetDB().Where("name = ?", "n2").First(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n.SyncMode != "bidirectional" {
		t.Fatalf("got %q", n.SyncMode)
	}
}

func TestNodeSyncModeRejectsUnknown(t *testing.T) {
	if err := database.InitDB(filepath.Join(t.TempDir(), "syncmode3.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.CloseDBForTest() })

	svc := NodeService{}
	payload, _ := json.Marshal(map[string]any{
		"enable": true, "name": "n3", "baseUrl": "https://127.0.0.1:2095",
		"webPath": "/app/", "token": "tok", "syncMode": "magic",
	})
	if err := svc.Save(database.GetDB(), "new", payload); err == nil {
		t.Fatal("expected error")
	}
}
