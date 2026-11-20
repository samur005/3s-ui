package xray

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/samur005/3s-ui/logger"
)

const (
	maxLogLines  = 500
	stopGrace    = 5 * time.Second
	restartDelay = 3 * time.Second
)

type logRing struct {
	mu   sync.Mutex
	rows []string
	buf  bytes.Buffer
}

func (r *logRing) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			r.buf.Write(p)
			break
		}
		r.buf.Write(p[:i])
		r.emitLocked()
		p = p[i+1:]
	}
	return n, nil
}

func (r *logRing) emitLocked() {
	line := string(bytes.TrimSpace(r.buf.Bytes()))
	r.buf.Reset()
	if line == "" {
		return
	}
	if len(r.rows) >= maxLogLines {
		r.rows = append(r.rows[:0], r.rows[len(r.rows)-maxLogLines+1:]...)
	}
	r.rows = append(r.rows, line)
	logger.Infof("xray | %s", line)
}

func (r *logRing) last(n int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n <= 0 || n > len(r.rows) {
		n = len(r.rows)
	}
	out := make([]string, n)
	copy(out, r.rows[len(r.rows)-n:])
	return out
}

// Manager supervises one Xray process.
type Manager struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	bin    string
	cfg    string
	logs   logRing
	want   bool
	err    string
	cancel context.CancelFunc
}

var global = &Manager{}

// Get returns the process manager singleton.
func Get() *Manager { return global }

// Status is what the API / UI reads.
type Status struct {
	Supported  bool     `json:"supported"`
	Running    bool     `json:"running"`
	Binary     string   `json:"binary"`
	BinaryOK   bool     `json:"binaryFound"`
	Version    string   `json:"version"`
	Pinned     string   `json:"pinned"`
	ConfigPath string   `json:"configPath"`
	Error      string   `json:"error,omitempty"`
	Log        []string `json:"log,omitempty"`
	Inbounds   int      `json:"inbounds"`
}

func (m *Manager) Status(inboundCount int) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := Status{Supported: true, Pinned: PinnedRelease, ConfigPath: m.cfg, Error: m.err, Inbounds: inboundCount}
	bin, err := FindBinary()
	if err == nil {
		st.Binary, st.BinaryOK, st.Version = bin, true, VersionOf(bin)
	} else if st.Error == "" {
		st.Error = err.Error()
	}
	st.Running = m.cmd != nil && m.cmd.Process != nil
	st.Log = m.logs.last(80)
	return st
}

// Apply rebuilds the config from the DB and (re)starts Xray when there is at
// least one enabled xvless inbound; otherwise it stops the process.
func (m *Manager) Apply(build func() ([]byte, error)) {
	raw, err := build()
	if err != nil {
		m.setErr(err.Error())
		logger.Warning("xray: build config: ", err)
		return
	}
	var doc struct {
		Inbounds []any `json:"inbounds"`
	}
	_ = json.Unmarshal(raw, &doc)
	path, err := WriteConfigFile(ConfigDir(), raw)
	if err != nil {
		m.setErr(err.Error())
		return
	}
	if len(doc.Inbounds) == 0 {
		m.Stop()
		m.setErr("")
		return
	}
	if err := m.TestConfig(path); err != nil {
		m.setErr(err.Error())
		logger.Warning("xray: config test failed: ", err)
		return
	}
	m.Restart(path)
}

// TestConfig runs `xray run -test -c path`.
func (m *Manager) TestConfig(path string) error {
	bin, err := FindBinary()
	if err != nil {
		return err
	}
	out, err := exec.Command(bin, "run", "-test", "-c", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("xray run -test: %w\n%s", err, bytes.TrimSpace(out))
	}
	return nil
}

func (m *Manager) setErr(s string) {
	m.mu.Lock()
	m.err = s
	m.mu.Unlock()
}

// Restart stops any running process and starts one with the given config.
func (m *Manager) Restart(cfgPath string) {
	m.Stop()
	bin, err := FindBinary()
	if err != nil {
		m.setErr(err.Error())
		return
	}
	m.mu.Lock()
	m.bin, m.cfg, m.want = bin, cfgPath, true
	m.mu.Unlock()
	m.start()
}

func (m *Manager) start() {
	m.mu.Lock()
	bin, cfg, want := m.bin, m.cfg, m.want
	m.mu.Unlock()
	if !want || bin == "" || cfg == "" {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, "run", "-c", cfg)
	cmd.Stdout = &m.logs
	cmd.Stderr = &m.logs
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		cancel()
		m.setErr(err.Error())
		logger.Warning("xray: start: ", err)
		return
	}
	m.mu.Lock()
	m.cmd, m.cancel, m.err = cmd, cancel, ""
	m.mu.Unlock()
	logger.Infof("xray: started pid %d (%s)", cmd.Process.Pid, VersionOf(bin))
	go m.wait(cmd, cancel)
}

func (m *Manager) wait(cmd *exec.Cmd, cancel context.CancelFunc) {
	err := cmd.Wait()
	cancel()
	m.mu.Lock()
	same := m.cmd == cmd
	if same {
		m.cmd = nil
	}
	want := m.want
	m.mu.Unlock()
	if !same {
		return
	}
	if err != nil {
		logger.Warning("xray: exited: ", err)
		m.setErr(err.Error())
	}
	if want {
		time.Sleep(restartDelay)
		m.mu.Lock()
		still := m.want && m.cmd == nil
		m.mu.Unlock()
		if still {
			logger.Info("xray: restarting")
			m.start()
		}
	}
}

// Stop ends the process.
func (m *Manager) Stop() {
	m.mu.Lock()
	m.want = false
	cmd, cancel := m.cmd, m.cancel
	m.cmd, m.cancel = nil, nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() {
			_, _ = cmd.Process.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(stopGrace):
			_ = cmd.Process.Kill()
		}
	}
}

// Shutdown is called when the panel exits.
func (m *Manager) Shutdown() { m.Stop() }
