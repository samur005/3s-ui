package database

import (
	"path/filepath"
	"testing"

	"github.com/samur005/3s-ui/database/model"
	"github.com/samur005/3s-ui/logger"

	"github.com/op/go-logging"
)

// An upgrade must not cut off nodes that were reached over a private network
// before the SSRF guard existed, and must run once: a node the operator later
// switches back to "public only" must stay that way across restarts.
func TestMigrateNodeAllowPrivate(t *testing.T) {
	logger.InitLogger(logging.ERROR)
	path := filepath.Join(t.TempDir(), "x.db")
	if err := InitDB(path); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = CloseDBForTest() })

	// Simulate a pre-upgrade database: drop the marker, seed a node with the
	// column at its default.
	if err := GetDB().Where("key = ?", migratedKeyNodeAllowPrivate).Delete(&model.Setting{}).Error; err != nil {
		t.Fatal(err)
	}
	n := model.Node{Name: "old", BaseUrl: "http://10.0.0.2:2095", WebPath: "/app/", Token: "t"}
	if err := GetDB().Create(&n).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateNodeAllowPrivate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var got model.Node
	GetDB().First(&got, n.Id)
	if !got.AllowPrivate {
		t.Fatal("existing node lost private-address access on upgrade")
	}

	// Second run is a no-op: an operator's later choice sticks.
	GetDB().Model(&got).Update("allow_private", false)
	if err := migrateNodeAllowPrivate(); err != nil {
		t.Fatal(err)
	}
	GetDB().First(&got, n.Id)
	if got.AllowPrivate {
		t.Fatal("migration ran twice and overrode the operator's setting")
	}

	// A node created after the upgrade starts from the safe default.
	fresh := model.Node{Name: "new", BaseUrl: "https://node.example", WebPath: "/app/", Token: "t"}
	GetDB().Create(&fresh)
	GetDB().First(&got, fresh.Id)
	if got.AllowPrivate {
		t.Fatal("new node defaulted to private-address access")
	}
}

// Reopening an upgraded database must be clean: AutoMigrate over the new
// columns and table is idempotent.
func TestNodeSchemaReopen(t *testing.T) {
	logger.InitLogger(logging.ERROR)
	path := filepath.Join(t.TempDir(), "y.db")
	if err := InitDB(path); err != nil {
		t.Fatalf("first InitDB: %v", err)
	}
	op := model.NodeOperation{NodeId: 1, RequestId: "r-1", Op: "health", Status: "ok", StartedAt: 1}
	if err := GetDB().Create(&op).Error; err != nil {
		t.Fatalf("insert operation: %v", err)
	}
	if err := CloseDBForTest(); err != nil {
		t.Fatal(err)
	}
	if err := InitDB(path); err != nil {
		t.Fatalf("second InitDB: %v", err)
	}
	t.Cleanup(func() { _ = CloseDBForTest() })
	var count int64
	GetDB().Model(model.NodeOperation{}).Count(&count)
	if count != 1 {
		t.Fatalf("operations lost across reopen: %d", count)
	}
	dup := model.NodeOperation{NodeId: 1, RequestId: "r-1", Op: "health", Status: "ok", StartedAt: 2}
	if err := GetDB().Create(&dup).Error; err == nil {
		t.Fatal("request id is not unique")
	}
}
