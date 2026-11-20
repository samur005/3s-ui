package service

import (
	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/logger"
	"github.com/samur005/3s-ui/qwdtt"

	"gorm.io/gorm"
)

// MigrateToInbound turns the configuration the standalone qWDTT page used to
// keep (qwdtt_states) into a qWDTT inbound, once, so a panel updated from
// v2.2.0 shows its running setup on the Inbounds page. The clients that had the
// link (everyone, or the named ones) are bound to the new inbound, which is how
// access is expressed now.
//
// It is idempotent and never fatal: with an inbound already there, or no old
// configuration, it does nothing; on failure the old copy stays for the next
// start. Once the inbound exists the old copy is cleared, so deleting the
// inbound later is not undone by a re-import.
func (s *QwdttService) MigrateToInbound() {
	if err := s.migrateToInbound(); err != nil {
		logger.Warning("qwdtt: moving the old settings into an inbound failed, will retry on the next start: ", err)
	}
}

func (s *QwdttService) migrateToInbound() error {
	cfg, mode, names, ok, err := qwdtt.LegacyConfig()
	if err != nil || !ok {
		return err
	}
	if qwdtt.InboundID() != 0 {
		return qwdtt.ClearLegacyConfig()
	}
	if _, err := qwdtt.Save(cfg); err != nil {
		return err
	}
	id := qwdtt.InboundID()
	if id == 0 {
		return nil
	}
	err = database.GetDB().Transaction(func(tx *gorm.DB) error {
		var ids []uint
		q := tx.Table("clients").Select("id")
		if mode == "selected" {
			if len(names) == 0 {
				return nil
			}
			q = q.Where("name IN ?", names)
		}
		if err := q.Scan(&ids).Error; err != nil {
			return err
		}
		_, err := (&ClientService{}).AddInboundsToClients(tx, ids, []uint{id}, "")
		return err
	})
	if err != nil {
		return err
	}
	logger.Info("qwdtt: moved the qWDTT settings into the inbound ", id)
	return qwdtt.ClearLegacyConfig()
}
