package qwdtt

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/op/go-logging"
	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/logger"
)

func TestMain(m *testing.M) {
	logger.InitLogger(logging.ERROR)
	os.Exit(m.Run())
}

// initDB opens a throwaway panel database and clears the environment
// override, which would otherwise leak in from the developer's shell.
func initDB(t *testing.T) {
	t.Helper()
	t.Setenv(EnvVkHash, "")
	dir := t.TempDir()
	t.Setenv("S3UI_DB_FOLDER", dir)
	if err := database.InitDB(filepath.Join(dir, "qwdtt.db")); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = database.CloseDBForTest() })
}
