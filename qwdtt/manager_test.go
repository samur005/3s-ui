package qwdtt

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeServer writes a stand-in for the qWDTT binary that records its argv and
// then idles, or exits at once when crash is set.
func fakeServer(t *testing.T, crash bool) (bin, argvFile string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("shell script stand-in needs Linux")
	}
	dir := t.TempDir()
	bin = filepath.Join(dir, "qwdtt")
	argvFile = filepath.Join(dir, "argv")
	body := "#!/bin/sh\necho \"$@\" > " + argvFile + "\necho started\n"
	if crash {
		body += "exit 3\n"
	} else {
		body += "exec sleep 300\n"
	}
	if err := os.WriteFile(bin, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvBin, bin)
	return bin, argvFile
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func newMgr() *Manager { return &Manager{} }

func direct(c *Config) {
	c.RouteThroughCore = false
	c.ConfigDir = ""
}

func TestManagerStartsRestartsAndStops(t *testing.T) {
	initDB(t)
	_, argvFile := fakeServer(t, false)
	m := newMgr()
	t.Cleanup(m.Shutdown)

	enable(t, direct)
	m.Reconcile()
	if !m.proc.running() {
		t.Fatalf("server not started: %+v", m.Status(Default()))
	}
	waitFor(t, "argv file", func() bool { _, err := os.Stat(argvFile); return err == nil })
	raw, _ := os.ReadFile(argvFile)
	for _, want := range []string{"-listen 0.0.0.0:56000", "-wg-port 56001", "-password ", "-config-dir " + filepath.Join(os.Getenv("S3UI_DB_FOLDER"), "qwdtt")} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("argv %q lacks %q", raw, want)
		}
	}
	pid := m.proc.pid()
	if pid == 0 {
		t.Fatal("no pid")
	}

	// Nothing changed: same process.
	m.Reconcile()
	if m.proc.pid() != pid {
		t.Error("an unchanged config restarted the server")
	}

	// A changed flag restarts it.
	enable(t, func(c *Config) { direct(c); c.WGPort = 56010 })
	m.Reconcile()
	waitFor(t, "restart", func() bool { return m.proc.running() && m.proc.pid() != pid })
	waitFor(t, "new argv", func() bool { raw, _ := os.ReadFile(argvFile); return strings.Contains(string(raw), "-wg-port 56010") })

	// Explicit restart.
	pid = m.proc.pid()
	m.Restart()
	if !m.proc.running() || m.proc.pid() == pid {
		t.Error("Restart did not replace the process")
	}

	// Logs are captured.
	waitFor(t, "log line", func() bool { return len(m.Logs(10)) > 0 })
	if got := m.Logs(10); got[0] != "started" {
		t.Errorf("logs = %v", got)
	}

	// Disabling stops it.
	c, _ := Load()
	c.Enabled = false
	if _, err := Save(c); err != nil {
		t.Fatal(err)
	}
	m.Reconcile()
	if m.proc.running() {
		t.Error("disabled server still running")
	}
	if st := m.Status(c); st.Running || st.Enabled {
		t.Errorf("status = %+v", st)
	}
}

func TestManagerReportsMissingBinary(t *testing.T) {
	initDB(t)
	t.Setenv(EnvBin, filepath.Join(t.TempDir(), "nope"))
	m := newMgr()
	if runtime.GOOS != "linux" {
		t.Skip("Linux only")
	}
	enable(t, direct)
	m.Reconcile()
	c, _ := Load()
	st := m.Status(c)
	if st.Running || st.BinaryFound || !strings.Contains(st.Error, "not found") {
		t.Errorf("status = %+v", st)
	}
}

func TestManagerPacesACrashLoop(t *testing.T) {
	initDB(t)
	fakeServer(t, true)
	m := newMgr()
	t.Cleanup(m.Shutdown)
	enable(t, direct)
	m.Reconcile()
	waitFor(t, "exit", func() bool { return !m.proc.running() })
	first := len(m.Logs(10))
	// An immediate second reconcile must not respawn it: the loop is paced.
	m.Reconcile()
	time.Sleep(100 * time.Millisecond)
	if got := len(m.Logs(10)); got != first {
		t.Errorf("server was respawned inside the pacing window (%d -> %d log lines)", first, got)
	}
	c, _ := Load()
	if st := m.Status(c); st.LastExit == "" {
		t.Errorf("last exit not reported: %+v", st)
	}
}

func TestManagerRefusesInvalidConfigWithoutRunning(t *testing.T) {
	initDB(t)
	fakeServer(t, false)
	m := newMgr()
	t.Cleanup(m.Shutdown)
	enable(t, direct)
	// Write an invalid row behind Save's back (Merge heals zero values, so use
	// an out-of-range one).
	if _, err := update(func(c *Config) error { c.ClientPort = 70000; return nil }); err != nil {
		t.Fatal(err)
	}
	m.Reconcile()
	if m.proc.running() {
		t.Error("server started from a config that fails validation")
	}
	c, _ := Load()
	if st := m.Status(c); !strings.Contains(st.Error, "clientPort") {
		t.Errorf("validation error not reported: %+v", st)
	}
}
