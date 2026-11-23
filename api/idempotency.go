package api

import (
	"bytes"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/samur005/3s-ui/util/common"

	"github.com/gin-gonic/gin"
)

// Idempotency and replay protection for apiv2 writes.
//
// A managing master sends every POST with X-Request-Id, and retries a write
// whose answer it lost with the same id. The node keeps the answer of each
// (token user, request id) for idempotencyTTL and replays it instead of
// running the write again, so "create inbound" retried after a timeout creates
// one inbound. X-Request-Timestamp, when sent, must be within maxClockSkew:
// a captured request cannot be replayed later.
//
// Both headers are optional: API clients that do not send them (scripts,
// older masters) behave exactly as before.

const (
	idempotencyTTL = 10 * time.Minute
	maxClockSkew   = 15 * time.Minute
	idempotencyMax = 4096
)

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)

type idemEntry struct {
	at       time.Time
	inflight bool
	status   int
	body     []byte
}

var (
	idemMu    sync.Mutex
	idemCache = map[string]*idemEntry{}
)

type captureWriter struct {
	gin.ResponseWriter
	buf bytes.Buffer
}

func (w *captureWriter) Write(b []byte) (int, error) {
	w.buf.Write(b)
	return w.ResponseWriter.Write(b)
}

func (w *captureWriter) WriteString(s string) (int, error) {
	w.buf.WriteString(s)
	return w.ResponseWriter.WriteString(s)
}

func evictIdem(now time.Time) {
	for k, e := range idemCache {
		if !e.inflight && now.Sub(e.at) > idempotencyTTL {
			delete(idemCache, k)
		}
	}
	// Still over the cap: drop the oldest finished entries.
	for len(idemCache) > idempotencyMax {
		var oldestK string
		var oldest time.Time
		for k, e := range idemCache {
			if e.inflight {
				continue
			}
			if oldestK == "" || e.at.Before(oldest) {
				oldestK, oldest = k, e.at
			}
		}
		if oldestK == "" {
			return
		}
		delete(idemCache, oldestK)
	}
}

// withIdempotency runs handler at most once per (user, X-Request-Id).
func withIdempotency(c *gin.Context, username string, handler func()) {
	if ts := c.GetHeader("X-Request-Timestamp"); ts != "" {
		sec, err := strconv.ParseInt(ts, 10, 64)
		if err != nil {
			jsonMsg(c, "", common.NewError("invalid X-Request-Timestamp"))
			return
		}
		if d := time.Since(time.Unix(sec, 0)); d > maxClockSkew || d < -maxClockSkew {
			jsonMsg(c, "", common.NewError("request timestamp outside the allowed window (replayed request or clock skew)"))
			return
		}
	}
	rid := c.GetHeader("X-Request-Id")
	if rid == "" {
		handler()
		return
	}
	if !requestIDPattern.MatchString(rid) {
		jsonMsg(c, "", common.NewError("invalid X-Request-Id"))
		return
	}
	key := username + "\x00" + c.Param("postAction") + "\x00" + rid
	now := time.Now()
	idemMu.Lock()
	if e, ok := idemCache[key]; ok && now.Sub(e.at) <= idempotencyTTL {
		if e.inflight {
			idemMu.Unlock()
			jsonMsg(c, "", common.NewError("a request with this X-Request-Id is still running"))
			return
		}
		status, body := e.status, e.body
		idemMu.Unlock()
		c.Header("X-Idempotent-Replay", "1")
		c.Data(status, "application/json; charset=utf-8", body)
		return
	}
	idemCache[key] = &idemEntry{at: now, inflight: true}
	if len(idemCache) > idempotencyMax {
		evictIdem(now)
	}
	idemMu.Unlock()

	cw := &captureWriter{ResponseWriter: c.Writer}
	c.Writer = cw
	handler()

	idemMu.Lock()
	status := cw.Status()
	if status == 0 {
		status = http.StatusOK
	}
	idemCache[key] = &idemEntry{at: time.Now(), status: status, body: append([]byte(nil), cw.buf.Bytes()...)}
	idemMu.Unlock()
}
