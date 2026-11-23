package qwdtt

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/logger"
)

// EnvBin points the panel at a qWDTT server binary in a non-standard place.
const EnvBin = "S3UI_QWDTT_BIN"

// BinaryPath finds the server binary: $S3UI_QWDTT_BIN, then bin/qwdtt and
// qwdtt next to the panel binary, then qwdtt in PATH. The first candidate
// that exists wins; when none does, the conventional location is returned so
// the error message tells the operator where to put it.
func BinaryPath() string {
	name := "qwdtt"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if p := strings.TrimSpace(os.Getenv(EnvBin)); p != "" {
		return p
	}
	var cands []string
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		cands = append(cands, filepath.Join(dir, "bin", name), filepath.Join(dir, name))
	}
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	if len(cands) > 0 {
		return cands[0]
	}
	return name
}

// Manager owns the server process and its routing.
type Manager struct {
	mu   sync.Mutex // serializes Reconcile / Restart / Shutdown
	proc proc

	stateMu    sync.Mutex
	lastError  string
	routed     bool
	nextStart  time.Time
	failStreak int
}

var defaultMgr = &Manager{}

// Get returns the process-wide manager.
func Get() *Manager { return defaultMgr }

func (m *Manager) setError(msg string) {
	m.stateMu.Lock()
	m.lastError = msg
	m.stateMu.Unlock()
}

// Reconcile brings the process and the routing in line with the stored
// configuration. It is called by the cron job, after every save and on start.
// It is cheap when nothing changed.
func (m *Manager) Reconcile() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reconcileLocked(false)
}

// Restart stops the server and starts it again.
func (m *Manager) Restart() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reconcileLocked(true)
}

// Shutdown stops the server and removes the routing; called on panel exit.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.proc.stop()
	clearRouting()
	m.stateMu.Lock()
	m.routed = false
	m.stateMu.Unlock()
}

func (m *Manager) reconcileLocked(force bool) {
	cfg, err := Load()
	if err != nil {
		m.setError("reading configuration: " + err.Error())
		return
	}
	if !cfg.Enabled {
		m.proc.stop()
		clearRouting()
		m.stateMu.Lock()
		m.routed, m.lastError, m.failStreak = false, "", 0
		m.stateMu.Unlock()
		return
	}
	if !RoutingSupported() {
		m.setError("qWDTT is only supported on Linux")
		return
	}
	if err := cfg.Validate(); err != nil {
		m.proc.stop()
		m.setError(err.Error())
		return
	}
	if strings.TrimSpace(cfg.Password) == "" {
		if c2, err := cfg.EnsurePassword(); err == nil {
			if saved, err := Save(c2); err == nil {
				cfg = saved
			}
		}
	}
	// Per-client passwords: passwords.json is written before a start, so the
	// server reads the users in with its first load; a running server re-reads
	// it on SIGHUP below.
	usersChanged := false
	if cfg.PerClient() {
		if err := os.MkdirAll(cfg.ResolveConfigDir(), 0o700); err == nil {
			if in, err := LoadInbound(database.GetDB()); err == nil && in != nil {
				changed, err := syncUsers(database.GetDB(), in.Id, cfg)
				if err != nil {
					logger.Warning("qwdtt: syncing client passwords: ", err)
				}
				usersChanged = changed
			}
		}
	}

	args := cfg.BuildArgs()
	fp := strings.Join(args, "\x00")

	m.proc.mu.Lock()
	changed := m.proc.argsFP != fp
	m.proc.mu.Unlock()
	if m.proc.running() && (force || changed) {
		m.proc.stop()
	}
	if !m.proc.running() {
		m.stateMu.Lock()
		wait := time.Until(m.nextStart)
		m.stateMu.Unlock()
		if wait > 0 && !force && !changed {
			return // a crash loop is paced, not hammered
		}
		if err := os.MkdirAll(cfg.ResolveConfigDir(), 0o700); err != nil {
			m.setError("creating config dir: " + err.Error())
			return
		}
		bin := BinaryPath()
		if err := m.proc.start(bin, args); err != nil {
			m.setError(err.Error())
			m.stateMu.Lock()
			m.failStreak++
			m.nextStart = time.Now().Add(restartDelay * time.Duration(min(m.failStreak, 6)))
			m.stateMu.Unlock()
			logger.Warning(err.Error())
			return
		}
		m.proc.mu.Lock()
		m.proc.argsFP = fp
		m.proc.mu.Unlock()
		m.stateMu.Lock()
		m.lastError = ""
		m.nextStart = time.Now().Add(restartDelay)
		m.stateMu.Unlock()
		logger.Infof("qwdtt: server started (%s)", bin)
		// The interfaces appear a moment after the process starts.
		if cfg.RouteThroughCore {
			waitForIface(ifaceWG, 3*time.Second)
		}
	} else {
		m.stateMu.Lock()
		m.failStreak = 0
		m.stateMu.Unlock()
		if usersChanged {
			m.proc.reload()
		}
	}

	if cfg.RouteThroughCore {
		ok := ensureRouting()
		m.stateMu.Lock()
		m.routed = ok
		m.stateMu.Unlock()
	} else {
		clearRouting()
		m.stateMu.Lock()
		m.routed = false
		m.stateMu.Unlock()
	}
}

// Status is the live state shown on the qWDTT page.
type Status struct {
	Supported   bool   `json:"supported"`
	Enabled     bool   `json:"enabled"`
	Running     bool   `json:"running"`
	Pid         int    `json:"pid"`
	Uptime      int64  `json:"uptime"`
	Binary      string `json:"binary"`
	BinaryFound bool   `json:"binaryFound"`
	Routed      bool   `json:"routed"`
	TunReady    bool   `json:"tunReady"`
	Error       string `json:"error,omitempty"`
	LastExit    string `json:"lastExit,omitempty"`
	HashEnv     bool   `json:"hashEnv"`
	HasCookies  bool   `json:"hasCookies"`
	// HasRemote is whether a WDTT panel is set as a hash source.
	HasRemote bool `json:"hasRemote"`
}

// Status snapshots the manager.
func (m *Manager) Status(cfg Config) Status {
	bin := BinaryPath()
	_, statErr := os.Stat(bin)
	st := Status{
		Supported:   RoutingSupported(),
		Enabled:     cfg.Enabled,
		Running:     m.proc.running(),
		Pid:         m.proc.pid(),
		Binary:      bin,
		BinaryFound: statErr == nil,
		HashEnv:     EnvHashOverride() != "",
		HasCookies:  HasCookies(),
		HasRemote:   HasRemote(),
	}
	if st.Running {
		m.proc.mu.Lock()
		st.Uptime = int64(time.Since(m.proc.started).Seconds())
		m.proc.mu.Unlock()
	}
	m.stateMu.Lock()
	st.Error, st.Routed = m.lastError, m.routed
	m.stateMu.Unlock()
	if err := m.proc.lastExit(); err != nil && !st.Running {
		st.LastExit = err.Error()
	}
	if cfg.Enabled && !st.BinaryFound && st.Error == "" {
		st.Error = fmt.Sprintf("server binary not found at %s", bin)
	}
	st.TunReady = RoutingSupported() && cfg.RouteThroughCore && linkReady(TunName)
	return st
}

// Logs returns up to n recent lines the server wrote.
func (m *Manager) Logs(n int) []string { return m.proc.logs.last(n) }
