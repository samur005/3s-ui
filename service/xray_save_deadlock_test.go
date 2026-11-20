package service

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
	"github.com/samur005/3s-ui/xray"
)

func freeListenPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

func xvlessPayload(tag string, port int) string {
	// Flat panel shape: UnmarshalJSON puts leftover fields into Options.
	raw, _ := json.Marshal(map[string]any{
		"type":             xray.TypeVLESS,
		"tag":              tag,
		"enable":           true,
		"listen":           "127.0.0.1",
		"listen_port":      port,
		"endpoint":         "127.0.0.1",
		"method":           "raw",
		"security":         "none",
		"decryption":       "none",
		"routeThroughCore": true,
	})
	return string(raw)
}

// Save of xvless used to deadlock: afterCommit → ApplyStored → RestartCore
// while Save still held startMu.RLock. Must return promptly.
func TestXvlessSaveDoesNotDeadlock(t *testing.T) {
	s := runningPanel(t)
	port := freeListenPort(t)
	before := queuedRestarts.Load()

	done := make(chan error, 1)
	go func() {
		done <- save(t, s, "inbounds", "new", xvlessPayload("xv-deadlock", port))
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("xvless save: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("xvless save deadlocked (Save RLock + ApplyStored RestartCore)")
	}

	// Async handoff restart must complete and leave the core up.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if queuedRestarts.Load() > before && s.CoreRunning() && !xray.CoreNeedsRestart() {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !s.CoreRunning() {
		t.Fatal("core not running after xvless save + queued restart")
	}
	if xray.CoreNeedsRestart() {
		t.Fatal("handoff fingerprint still dirty after restart")
	}
	var n int64
	if err := database.GetDB().Model(model.Inbound{}).Where("type = ? AND tag = ?", xray.TypeVLESS, "xv-deadlock").Count(&n).Error; err != nil || n != 1 {
		t.Fatalf("xvless row: n=%d err=%v", n, err)
	}
}

func TestXvlessEditAndDeleteDoNotDeadlock(t *testing.T) {
	s := runningPanel(t)
	port := freeListenPort(t)
	if err := save(t, s, "inbounds", "new", xvlessPayload("xv-lifecycle", port)); err != nil {
		t.Fatalf("new: %v", err)
	}
	waitQueuedRestarts(t, s, 15*time.Second)

	var row model.Inbound
	if err := database.GetDB().Where("tag = ?", "xv-lifecycle").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	port2 := freeListenPort(t)
	edit := fmt.Sprintf(`{"id":%d,"type":%q,"tag":"xv-lifecycle","enable":true,"listen":"127.0.0.1","listen_port":%d,"endpoint":"127.0.0.1","method":"raw","security":"none","decryption":"none","routeThroughCore":true}`,
		row.Id, xray.TypeVLESS, port2)

	done := make(chan error, 1)
	go func() { done <- save(t, s, "inbounds", "edit", edit) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("edit: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("xvless edit deadlocked")
	}
	waitQueuedRestarts(t, s, 15*time.Second)

	done = make(chan error, 1)
	go func() { done <- save(t, s, "inbounds", "del", `"xv-lifecycle"`) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("del: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("xvless delete deadlocked")
	}
	waitQueuedRestarts(t, s, 15*time.Second)
	if !s.CoreRunning() {
		t.Fatal("core down after xvless delete")
	}
}

// Mixed sing-box inbound saves still complete under the same Save path and
// must not hang when an xvless handoff restart may also be queued.
func TestMixedInboundSaveWithXvlessPresent(t *testing.T) {
	s := runningPanel(t)
	if err := save(t, s, "inbounds", "new", xvlessPayload("xv-with-mixed", freeListenPort(t))); err != nil {
		t.Fatalf("xvless: %v", err)
	}
	waitQueuedRestarts(t, s, 15*time.Second)

	done := make(chan error, 1)
	go func() {
		done <- save(t, s, "inbounds", "new",
			`{"type":"mixed","tag":"mix-alive","listen":"127.0.0.1","listen_port":0}`)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("mixed save: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("mixed inbound save hung")
	}
	if !s.CoreRunning() {
		t.Fatal("core down after mixed save")
	}
}

func waitQueuedRestarts(t *testing.T, s *ConfigService, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if !restartQueued.Load() && s.CoreRunning() {
			// Give a moment for fingerprint update after startLocked.
			time.Sleep(100 * time.Millisecond)
			if !restartQueued.Load() {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("queued restart did not settle; queued=%v running=%v needsRestart=%v",
		restartQueued.Load(), s.CoreRunning(), xray.CoreNeedsRestart())
}

func TestApplyStoredUnderHoldCoreDoesNotDeadlock(t *testing.T) {
	// Direct unit check of the historic failure mode: RLock held + ApplyStored.
	s := runningPanel(t)
	_ = s
	port := freeListenPort(t)
	// Seed an xvless row so CoreNeedsRestart becomes true after inject fingerprint mismatch.
	if err := save(t, s, "inbounds", "new", xvlessPayload("xv-hold", port)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	waitQueuedRestarts(t, s, 15*time.Second)

	// Force dirty handoff fingerprint without going through Save.
	xray.ResetCoreFingerprintForTest()

	done := make(chan struct{})
	go func() {
		defer close(done)
		HoldCore(func() {
			(&XrayService{}).ApplyStored()
		})
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("ApplyStored under HoldCore (RLock) deadlocked")
	}
}

func init() {
	// Prefer pinned test binary when present (handoff e2e installs it).
	if os.Getenv("S3UI_XRAY_BIN") == "" {
		for _, p := range []string{"/tmp/xraybin/xray", filepath.Join(os.TempDir(), "xray")} {
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				_ = os.Setenv("S3UI_XRAY_BIN", p)
				break
			}
		}
	}
}
