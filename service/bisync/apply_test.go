package bisync

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
)

func setupBisyncDB(t *testing.T) {
	t.Helper()
	ResetPanelSyncIDForTest()
	if err := database.InitDB(filepath.Join(t.TempDir(), "bisync.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = database.CloseDBForTest()
		ResetPanelSyncIDForTest()
	})
}

func TestApplyClientCreateAndConflict(t *testing.T) {
	setupBisyncDB(t)
	pid, err := PanelSyncID()
	if err != nil {
		t.Fatal(err)
	}
	rec := Record{
		UID: "u1", Kind: "client", Rev: 1, Base: 0, Origin: "other",
		Payload: map[string]any{"name": "alice", "enable": true, "config": map[string]any{}, "inboundsSync": []any{}},
	}
	if err := ApplyClientRecord(rec); err != nil {
		t.Fatal(err)
	}
	local := Record{UID: "u1", Kind: "client", Rev: 2, Base: 1, Origin: pid}
	remote := Record{UID: "u1", Kind: "client", Rev: 2, Base: 1, Origin: "other",
		Payload: map[string]any{"name": "alice2", "enable": true}}
	res := MergeRemote(pid, true, local, remote)
	if res.Action != "conflict" {
		t.Fatalf("want conflict got %s", res.Action)
	}
	if err := StoreConflict(1, res.Conflict); err != nil {
		t.Fatal(err)
	}
	rows, err := ListConflicts()
	if err != nil || len(rows) != 1 {
		t.Fatalf("conflicts %v %v", rows, err)
	}
	if err := ResolveConflict(rows[0].Id, KeepRemote); err != nil {
		t.Fatal(err)
	}
	var c model.Client
	if err := database.GetDB().Where("sync_uid = ?", "u1").First(&c).Error; err != nil {
		t.Fatal(err)
	}
	if c.Name != "alice2" {
		t.Fatalf("name %s", c.Name)
	}
}

func TestInboundTemplateKeepsLocalListen(t *testing.T) {
	setupBisyncDB(t)
	opts, _ := json.Marshal(map[string]any{"listen": "10.0.0.1", "listen_port": 8443, "flow": "old"})
	ib := model.Inbound{
		Type: "vless", Tag: "v1", Enable: true, Options: opts, OutJson: json.RawMessage(`{}`),
		Addrs: json.RawMessage(`[]`), SyncUID: "ib1", SyncRev: 1, SyncOrigin: "a",
	}
	if err := database.GetDB().Create(&ib).Error; err != nil {
		t.Fatal(err)
	}
	rec := Record{
		UID: "ib1", Kind: "inbound", Rev: 2, Base: 1, Origin: "b",
		Payload: map[string]any{
			"type": "vless", "tag": "v1", "enable": true,
			"options":  map[string]any{"flow": "xtls-rprx-vision"},
			"out_json": map[string]any{"flow": "xtls-rprx-vision"},
		},
	}
	if err := ApplyInboundRecord(rec); err != nil {
		t.Fatal(err)
	}
	var got model.Inbound
	if err := database.GetDB().First(&got, ib.Id).Error; err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(got.Options, &m)
	if m["listen"] != "10.0.0.1" || int(m["listen_port"].(float64)) != 8443 {
		t.Fatalf("local listen lost: %#v", m)
	}
	if m["flow"] != "xtls-rprx-vision" {
		t.Fatalf("flow not updated: %#v", m)
	}
}

func TestTombstoneClient(t *testing.T) {
	setupBisyncDB(t)
	rec := Record{
		UID: "u2", Kind: "client", Rev: 1, Origin: "other",
		Payload: map[string]any{"name": "bob", "enable": true, "config": map[string]any{}},
	}
	if err := ApplyClientRecord(rec); err != nil {
		t.Fatal(err)
	}
	tomb := Record{UID: "u2", Kind: "client", Rev: 2, Base: 1, Origin: "other", DeletedAt: 99}
	if err := ApplyClientRecord(tomb); err != nil {
		t.Fatal(err)
	}
	var c model.Client
	if err := database.GetDB().Where("sync_uid = ?", "u2").First(&c).Error; err != nil {
		t.Fatal(err)
	}
	if c.DeletedAt != 99 || c.Enable {
		t.Fatalf("%+v", c)
	}
}

func TestIngestIgnoresEcho(t *testing.T) {
	setupBisyncDB(t)
	pid, err := PanelSyncID()
	if err != nil {
		t.Fatal(err)
	}
	rec := Record{
		UID: "u3", Kind: "client", Rev: 1, Origin: pid,
		Payload: map[string]any{"name": "echo", "enable": true, "config": map[string]any{}},
	}
	cat := &Catalog{PanelID: pid, Proto: ProtocolVersion, Clients: []Record{rec}}
	if err := IngestRemoteCatalog(0, cat); err != nil {
		t.Fatal(err)
	}
	var n int64
	database.GetDB().Model(&model.Client{}).Where("sync_uid = ?", "u3").Count(&n)
	if n != 0 {
		t.Fatalf("echo should not create client, got %d", n)
	}
}
