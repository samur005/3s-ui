package api

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func idemRouter(runs *atomic.Int32) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/apiv2/:postAction", func(c *gin.Context) {
		withIdempotency(c, "admin", func() {
			n := runs.Add(1)
			c.JSON(http.StatusOK, gin.H{"success": true, "obj": n})
		})
	})
	return r
}

func idemPost(r *gin.Engine, action string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/apiv2/"+action, strings.NewReader(""))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func resetIdem(t *testing.T) {
	idemMu.Lock()
	idemCache = map[string]*idemEntry{}
	idemMu.Unlock()
	t.Cleanup(func() {
		idemMu.Lock()
		idemCache = map[string]*idemEntry{}
		idemMu.Unlock()
	})
}

// A write retried with the same request id runs once and replays the answer.
func TestIdempotentReplay(t *testing.T) {
	resetIdem(t)
	var runs atomic.Int32
	r := idemRouter(&runs)
	h := map[string]string{"X-Request-Id": "abcdef0123456789"}

	first := idemPost(r, "save", h)
	second := idemPost(r, "save", h)
	if runs.Load() != 1 {
		t.Fatalf("handler ran %d times, want 1", runs.Load())
	}
	if first.Body.String() != second.Body.String() || second.Header().Get("X-Idempotent-Replay") != "1" {
		t.Fatalf("replay = %q (replay header %q), want %q", second.Body.String(), second.Header().Get("X-Idempotent-Replay"), first.Body.String())
	}
	// The same id on another action is a different request.
	idemPost(r, "restartSb", h)
	// No id: no dedupe (scripts and old masters behave as before).
	idemPost(r, "save", nil)
	idemPost(r, "save", nil)
	if runs.Load() != 4 {
		t.Fatalf("handler ran %d times, want 4", runs.Load())
	}
}

func TestIdempotencyRejectsBadHeaders(t *testing.T) {
	resetIdem(t)
	var runs atomic.Int32
	r := idemRouter(&runs)
	cases := map[string]map[string]string{
		"short id":        {"X-Request-Id": "abc"},
		"id with spaces":  {"X-Request-Id": "abc def ghi jkl"},
		"bad timestamp":   {"X-Request-Timestamp": "yesterday"},
		"replayed (old)":  {"X-Request-Timestamp": strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10)},
		"from the future": {"X-Request-Timestamp": strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)},
	}
	for name, h := range cases {
		w := idemPost(r, "save", h)
		if !strings.Contains(w.Body.String(), `"success":false`) {
			t.Errorf("%s: body %s, want a refusal", name, w.Body.String())
		}
	}
	if runs.Load() != 0 {
		t.Fatalf("handler ran %d times for refused requests", runs.Load())
	}
	ok := idemPost(r, "save", map[string]string{"X-Request-Timestamp": strconv.FormatInt(time.Now().Unix(), 10)})
	if runs.Load() != 1 || !strings.Contains(ok.Body.String(), `"success":true`) {
		t.Fatalf("fresh timestamp refused: %s", ok.Body.String())
	}
}

func TestIdempotencyCacheIsBounded(t *testing.T) {
	resetIdem(t)
	old := time.Now().Add(-2 * idempotencyTTL)
	idemMu.Lock()
	for i := 0; i < idempotencyMax+10; i++ {
		idemCache["k"+strconv.Itoa(i)] = &idemEntry{at: old}
	}
	idemCache["running"] = &idemEntry{at: old, inflight: true}
	evictIdem(time.Now())
	n := len(idemCache)
	_, kept := idemCache["running"]
	idemMu.Unlock()
	if n > idempotencyMax || !kept {
		t.Fatalf("after eviction: %d entries, in-flight kept=%v", n, kept)
	}
}
