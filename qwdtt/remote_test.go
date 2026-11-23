package qwdtt

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeWdtt answers like ildarmaga/wdtt's panel: /login sets the session and
// CSRF cookies, and vk/call/create wants both.
func fakeWdtt(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/wdtt/login", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("username") != "admin" || r.Form.Get("password") != "pw" {
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "msg": "wrong"})
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "wdtt-session", Value: "s1"})
		http.SetCookie(w, &http.Cookie{Name: "wdtt-csrf", Value: "c1"})
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
	})
	mux.HandleFunc("/wdtt/panel/api/vk/call/create", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("wdtt-session"); err != nil || c.Value != "s1" || r.Header.Get("X-CSRF-Token") != "c1" {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "msg": "invalid csrf token"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "obj": map[string]any{
			"join_link": "https://vk.ru/call/join/remoteHash1", "vk_hash": "remoteHash1",
		}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestRemoteHashSource(t *testing.T) {
	initDB(t)
	t.Setenv(EnvWdttURL, "")
	t.Setenv("LUCX_WDTT_URL", "")
	srv := fakeWdtt(t)
	if CanGenerate() {
		t.Fatal("a hash source with nothing configured")
	}
	if err := SaveRemote(RemoteSource{URL: "ftp://x"}); err == nil {
		t.Error("a non-http URL was accepted")
	}
	if err := SaveRemote(RemoteSource{URL: srv.URL + "/wdtt/", Username: "admin", Password: "pw"}); err != nil {
		t.Fatal(err)
	}
	// Saving again without the password keeps it.
	if err := SaveRemote(RemoteSource{URL: srv.URL + "/wdtt", Username: "admin"}); err != nil {
		t.Fatal(err)
	}
	rs := LoadRemote()
	if rs.Password != "pw" || rs.URL != srv.URL+"/wdtt" || !CanGenerate() {
		t.Fatalf("stored = %+v", rs)
	}
	if v := rs.View(); !v.HasPassword || v.URL == "" {
		t.Errorf("view = %+v", v)
	}

	cfg := Default()
	cfg.Enabled = true
	if _, err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	changed, err := EnsureHashesNow()
	if err != nil || !changed {
		t.Fatalf("EnsureHashesNow = %v, %v", changed, err)
	}
	got, _ := Load()
	if got.VkHashes != "remoteHash1" {
		t.Errorf("hashes = %q", got.VkHashes)
	}
	calls, _ := loadCalls()
	if len(calls) != 1 || !calls[0].Remote || calls[0].CallID != "" {
		t.Errorf("calls = %+v", calls)
	}

	// Wrong password: the WDTT refusal is reported.
	_ = SaveRemote(RemoteSource{URL: srv.URL + "/wdtt", Username: "admin", Password: "bad"})
	if _, err := remoteCreateHash(LoadRemote()); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Errorf("err = %v", err)
	}

	// LucX's variables are honoured.
	t.Setenv("LUCX_WDTT_URL", "https://lucx.example/wdtt")
	if rs := LoadRemote(); !rs.FromEnv || rs.URL != "https://lucx.example/wdtt" {
		t.Errorf("env = %+v", rs)
	}
}
