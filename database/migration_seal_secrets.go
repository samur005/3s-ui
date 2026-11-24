package database

import (
	"path"

	"github.com/samur005/3s-ui/logger"
	"github.com/samur005/3s-ui/util/secretbox"
)

// SecretKeyPath is the sealing key's file, next to the database.
func SecretKeyPath(dbPath string) string { return path.Join(path.Dir(dbPath), "secret.key") }

// loadSecretKey is not fatal: without a key the panel keeps working with
// plaintext node secrets exactly as before, and says so in the log.
func loadSecretKey(dbPath string) {
	if err := secretbox.LoadOrCreate(SecretKeyPath(dbPath)); err != nil {
		logger.Warning("secrets: node tokens stay unencrypted at rest: ", err)
	}
}

// migrateSealSecrets rewrites node tokens and mTLS keys still stored in
// plaintext (rows from before encryption at rest) sealed. Idempotent by the
// "enc:v1:" prefix, so it runs on every start and costs one small scan.
func migrateSealSecrets() error {
	if !secretbox.Ready() {
		return nil
	}
	type row struct {
		Id        uint
		Token     string
		ClientKey string
	}
	var rows []row
	if err := db.Table("nodes").Select("id", "token", "client_key").Find(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		updates := map[string]any{}
		if r.Token != "" && !secretbox.IsSealed(r.Token) {
			v, err := secretbox.Seal(r.Token)
			if err != nil {
				return err
			}
			updates["token"] = v
		}
		if r.ClientKey != "" && !secretbox.IsSealed(r.ClientKey) {
			v, err := secretbox.Seal(r.ClientKey)
			if err != nil {
				return err
			}
			updates["client_key"] = v
		}
		if len(updates) > 0 {
			if err := db.Table("nodes").Where("id = ?", r.Id).Updates(updates).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

// UnsealNodeSecrets writes every sealed node secret back as plaintext -- the
// downgrade path (`3sui secrets -decrypt`) for going back to a release that
// does not know about encryption at rest.
func UnsealNodeSecrets() (int, error) {
	type row struct {
		Id        uint
		Token     string
		ClientKey string
	}
	var rows []row
	if err := db.Table("nodes").Select("id", "token", "client_key").Find(&rows).Error; err != nil {
		return 0, err
	}
	n := 0
	for _, r := range rows {
		updates := map[string]any{}
		for col, v := range map[string]string{"token": r.Token, "client_key": r.ClientKey} {
			if !secretbox.IsSealed(v) {
				continue
			}
			plain, err := secretbox.Open(v)
			if err != nil {
				return n, err
			}
			updates[col] = plain
		}
		if len(updates) > 0 {
			if err := db.Table("nodes").Where("id = ?", r.Id).Updates(updates).Error; err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}

// MigrateSealSecrets runs the sealing migration on demand (tests, CLI).
func MigrateSealSecrets() error { return migrateSealSecrets() }
