package qwdtt

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/samur005/3s-ui/fdguard"
	"github.com/samur005/3s-ui/logger"
)

const (
	maxLogLines  = 500
	maxPartial   = 64 << 10
	stopGrace    = 5 * time.Second
	stopForce    = 2 * time.Second
	restartDelay = 5 * time.Second
)

// logRing keeps the last lines the server wrote and forwards each one to the
// panel log, so startup failures show up in the log viewer and journald.
type logRing struct {
	mu      sync.Mutex
	rows    []string
	partial strings.Builder
}

func (r *logRing) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			r.partial.Write(p)
			break
		}
		r.partial.Write(p[:i])
		r.emitLocked()
		p = p[i+1:]
	}
	if r.partial.Len() >= maxPartial {
		r.emitLocked()
	}
	return n, nil
}

func (r *logRing) emitLocked() {
	line := strings.TrimSpace(strings.TrimRight(r.partial.String(), "\r"))
	r.partial.Reset()
	if line == "" {
		return
	}
	if len(r.rows) >= maxLogLines {
		r.rows = append(r.rows[:0], r.rows[len(r.rows)-maxLogLines+1:]...)
	}
	r.rows = append(r.rows, line)
	logger.Infof("qwdtt | %s", line)
}

func (r *logRing) flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.partial.Len() > 0 {
		r.emitLocked()
	}
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

// proc supervises the server process. It never restarts it by itself: the
// manager's reconcile loop does, so a crash loop is paced by the loop.
type proc struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	done    chan struct{}
	exitErr error
	started time.Time
	argsFP  string
	logs    logRing
}

func (p *proc) running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.runningLocked()
}

func (p *proc) runningLocked() bool {
	if p.cmd == nil || p.done == nil {
		return false
	}
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

func (p *proc) pid() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.runningLocked() && p.cmd.Process != nil {
		return p.cmd.Process.Pid
	}
	return 0
}

func (p *proc) start(bin string, args []string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.runningLocked() {
		return errors.New("qwdtt: server is already running")
	}
	if st, err := os.Stat(bin); err != nil || st.IsDir() {
		return fmt.Errorf("qwdtt: server binary not found at %s", bin)
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdout = &p.logs
	cmd.Stderr = &p.logs
	setProcAttr(cmd)
	// Prevent inheriting sing-box TUN fds (no O_CLOEXEC in sing-tun).
	fdguard.BeforeChildStart()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("qwdtt: starting server: %w", err)
	}
	done := make(chan struct{})
	p.cmd, p.done, p.exitErr, p.started = cmd, done, nil, time.Now()
	go func() {
		err := cmd.Wait()
		p.logs.flush()
		p.mu.Lock()
		if p.cmd == cmd {
			p.exitErr = err
		}
		p.mu.Unlock()
		close(done)
	}()
	return nil
}

// stop asks the server to exit and kills it after a grace period.
func (p *proc) stop() {
	p.mu.Lock()
	cmd, done := p.cmd, p.done
	alive := p.runningLocked()
	p.mu.Unlock()
	if !alive {
		return
	}
	terminate(cmd)
	select {
	case <-done:
		return
	case <-time.After(stopGrace):
	}
	kill(cmd)
	select {
	case <-done:
	case <-time.After(stopForce):
		logger.Warning("qwdtt: server did not exit after kill")
	}
}

func (p *proc) lastExit() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exitErr
}

// reload tells a running server to re-read passwords.json (SIGHUP).
func (p *proc) reload() {
	p.mu.Lock()
	cmd := p.cmd
	alive := p.runningLocked()
	p.mu.Unlock()
	if alive {
		hangup(cmd)
	}
}
