package database

import (
	"log"

	"github.com/samur005/3s-ui/database/model"

	"gorm.io/gorm"
)

const migratedKeyNodeAllowPrivate = "migratedNodeAllowPrivate"

// migrateNodeAllowPrivate keeps every node that existed before the master's
// SSRF guard reachable.
//
// The guard (service/nodeNet.go) refuses to dial a node whose address resolves
// to loopback or a private range unless the node row allows it, and the new
// allow_private column defaults to false. A cluster built on a private network
// (10.x, a WireGuard mesh, a node on the same host) would go offline on the
// first heartbeat after the upgrade. So, once: every node already present is
// allowed, preserving what it did before; nodes added afterwards start from the
// safe default and the operator opts in per node.
//
// One-shot like the other post-AutoMigrate migrations: it marks itself done
// with its own settings row and costs one indexed lookup per start after that.
// On a fresh database there are no nodes, so it only writes the marker.
func migrateNodeAllowPrivate() error {
	var flag model.Setting
	err := db.Where("key = ?", migratedKeyNodeAllowPrivate).First(&flag).Error
	if err == nil {
		return nil
	}
	if err != gorm.ErrRecordNotFound {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(model.Node{}).Where("allow_private = ?", false).Update("allow_private", true)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			log.Printf("nodes: %d existing node(s) keep private-address access after the SSRF guard upgrade; "+
				"turn \"Allow private address\" off on the Nodes page for any that are reached over the public internet",
				res.RowsAffected)
		}
		return tx.Create(&model.Setting{Key: migratedKeyNodeAllowPrivate, Value: "true"}).Error
	})
}
