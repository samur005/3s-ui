package api

import (
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
)

// A retried write is answered from the database after a restart (the
// in-memory cache is gone), and the stored answer is hashed and sealed.
func TestIdempotencySurvivesRestart(t *testing.T) {
	if err := database.InitDB(filepath.Join(t.TempDir(), "idem.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.CloseDBForTest() })
	resetIdem(t)
	var runs atomic.Int32
	r := idemRouter(&runs)
	hdr := map[string]string{"X-Request-Id": "persist-req-0001"}
	first := idemPost(r, "save", hdr)
	if first.Code != 200 || runs.Load() != 1 {
		t.Fatalf("first: %d runs=%d", first.Code, runs.Load())
	}
	var rec model.IdempotencyRecord
	if err := database.GetDB().First(&rec).Error; err != nil {
		t.Fatalf("no persisted record: %v", err)
	}
	if strings.Contains(rec.Key, "persist-req") || !strings.HasPrefix(rec.Body, "enc:v1:") {
		t.Fatalf("record not hashed/sealed: %+v", rec)
	}
	idemMu.Lock()
	idemCache = map[string]*idemEntry{}
	idemMu.Unlock()
	again := idemPost(r, "save", hdr)
	if runs.Load() != 1 || again.Body.String() != first.Body.String() {
		t.Fatalf("replay after restart re-ran the write: runs=%d body=%s", runs.Load(), again.Body.String())
	}
}
