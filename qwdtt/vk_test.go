package qwdtt

import (
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// fakeVK stands in for VK: it answers by endpoint and counts the calls.
type fakeVK struct {
	mu        sync.Mutex
	calls     []string
	alive     map[string]bool // join link hash -> running
	netDown   bool
	nextCall  int
	finished  []string
	noToken   bool
	startFail bool
}

func (f *fakeVK) install(t *testing.T) {
	t.Helper()
	old := vkPost
	t.Cleanup(func() { vkPost = old })
	vkPost = func(endpoint string, form url.Values, headers map[string]string) ([]byte, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls = append(f.calls, endpoint)
		if f.netDown {
			return nil, errors.New("i/o timeout")
		}
		switch {
		case strings.Contains(endpoint, "act=web_token"):
			if f.noToken || !strings.Contains(headers["Cookie"], "remixsid=") {
				return []byte(`{"data":{}}`), nil
			}
			return []byte(`{"data":{"access_token":"tok"}}`), nil
		case strings.Contains(endpoint, "act=get_anonym_token"):
			return []byte(`{"data":{"access_token":"anon"}}`), nil
		case strings.HasSuffix(endpoint, "/users.get"):
			return []byte(`{"response":[{"id":42}]}`), nil
		case strings.HasSuffix(endpoint, "/calls.start"):
			if f.startFail {
				return []byte(`{"error":{"error_code":9,"error_msg":"flood"}}`), nil
			}
			f.nextCall++
			id := strings.Repeat("a", 35) + string(rune('0'+f.nextCall))
			hash := "hash" + string(rune('0'+f.nextCall))
			if f.alive != nil {
				f.alive[hash] = true
			}
			return []byte(`{"response":{"call_id":"` + id + `","join_link":"https://vk.ru/call/join/` + hash + `"}}`), nil
		case strings.HasSuffix(endpoint, "/calls.forceFinish"):
			f.finished = append(f.finished, form.Get("call_id"))
			return []byte(`{"response":1}`), nil
		case strings.HasSuffix(endpoint, "/calls.getCallPreview"):
			link := form.Get("vk_join_link")
			hash := link[strings.LastIndex(link, "/")+1:]
			if f.alive[hash] {
				return []byte(`{"response":{"ok_join_link":"` + hash + `"}}`), nil
			}
			return []byte(`{"error":{"error_code":100,"error_msg":"call not found"}}`), nil
		}
		return nil, errors.New("unexpected endpoint " + endpoint)
	}
}

const goodCookies = `[{"name":"remixsid","value":"abc"},{"name":"remixlang","value":"0"}]`

func TestParseCookies(t *testing.T) {
	canon, err := ParseCookies(goodCookies)
	if err != nil || !strings.Contains(canon, `"remixsid"`) {
		t.Fatalf("JSON cookies rejected: %q, %v", canon, err)
	}
	header, err := cookieHeader(canon)
	if err != nil || header != "remixsid=abc; remixlang=0" {
		t.Errorf("cookie header = %q, %v", header, err)
	}
	canon, err = ParseCookies("Cookie: remixsid=abc; remixlang=0")
	if err != nil {
		t.Fatalf("raw header rejected: %v", err)
	}
	if h, _ := cookieHeader(canon); h != "remixsid=abc; remixlang=0" {
		t.Errorf("raw header round trip = %q", h)
	}
	for name, in := range map[string]string{
		"empty":        " ",
		"bad json":     "[{",
		"no session":   `[{"name":"remixlang","value":"0"}]`,
		"empty sid":    `[{"name":"remixsid","value":""}]`,
		"header split": `[{"name":"remixsid","value":"a\r\nX: y"}]`,
		"semicolon":    `[{"name":"remixsid","value":"a; b=c"}]`,
		"no pairs":     "garbage",
	} {
		if _, err := ParseCookies(in); err == nil {
			t.Errorf("%s: accepted %q", name, in)
		}
	}
}

func TestCookiesStoredWithoutLeaking(t *testing.T) {
	initDB(t)
	if HasCookies() {
		t.Fatal("fresh install has cookies")
	}
	if err := SaveCookies("nonsense"); err == nil {
		t.Fatal("bad cookies saved")
	}
	if err := SaveCookies(goodCookies); err != nil {
		t.Fatal(err)
	}
	if !HasCookies() {
		t.Fatal("cookies not stored")
	}
	if err := ClearCookies(); err != nil || HasCookies() {
		t.Fatalf("ClearCookies: %v, still stored=%v", err, HasCookies())
	}
}

func TestCreateCallFollowsTheVKFlow(t *testing.T) {
	f := &fakeVK{alive: map[string]bool{}}
	f.install(t)
	call, err := createCall("remixsid=abc")
	if err != nil {
		t.Fatal(err)
	}
	if call.Hash != "hash1" || call.JoinLink != "https://vk.ru/call/join/hash1" || len(call.CallID) != 36 {
		t.Errorf("call = %+v", call)
	}
	var order []string
	for _, c := range f.calls {
		order = append(order, c[strings.LastIndexAny(c, "/=")+1:])
	}
	if strings.Join(order, ",") != "web_token,users.get,calls.start" {
		t.Errorf("call order = %v", order)
	}
}

func TestCreateCallFailures(t *testing.T) {
	f := &fakeVK{}
	f.install(t)
	f.noToken = true
	if _, err := createCall("remixsid=abc"); err == nil || !strings.Contains(err.Error(), "no token") {
		t.Errorf("expired cookies: %v", err)
	}
	f.noToken, f.startFail = false, true
	if _, err := createCall("remixsid=abc"); err == nil || !strings.Contains(err.Error(), "VK API 9") {
		t.Errorf("API error not surfaced: %v", err)
	}
}

func TestCallAliveDistinguishesDeadFromUnknown(t *testing.T) {
	f := &fakeVK{alive: map[string]bool{"live": true}}
	f.install(t)
	if ok, err := CallAlive("https://vk.com/call/join/live"); err != nil || !ok {
		t.Errorf("live call = %v, %v", ok, err)
	}
	if ok, err := CallAlive("gone"); err != nil || ok {
		t.Errorf("ended call = %v, %v; VK answered, so err must be nil", ok, err)
	}
	f.netDown = true
	if _, err := CallAlive("live"); err == nil {
		t.Error("a network failure must be an error, not 'dead'")
	}
	if _, err := CallAlive(""); err == nil {
		t.Error("empty hash must be an error")
	}
}

func enable(t *testing.T, mut func(*Config)) {
	t.Helper()
	c := Default()
	c.Enabled = true
	if mut != nil {
		mut(&c)
	}
	if _, err := Save(c); err != nil {
		t.Fatal(err)
	}
}

func TestGenerateHashAddsAndCapsTheList(t *testing.T) {
	initDB(t)
	f := &fakeVK{alive: map[string]bool{}}
	f.install(t)
	enable(t, nil)

	if _, _, err := GenerateHash(false); err == nil || !strings.Contains(err.Error(), "cookies") {
		t.Fatalf("no cookies: %v", err)
	}
	if err := SaveCookies(goodCookies); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= MaxHashes; i++ {
		cfg, call, err := GenerateHash(false)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(cfg.VkHashes, call.Hash) {
			t.Fatalf("hash %s not added: %q", call.Hash, cfg.VkHashes)
		}
	}
	cfg, _ := Load()
	if cfg.VkHashes != "hash1,hash2,hash3,hash4" {
		t.Fatalf("hashes = %q", cfg.VkHashes)
	}
	callsBefore := len(f.calls)
	if _, _, err := GenerateHash(false); err == nil {
		t.Fatal("a fifth hash must be refused without replace")
	}
	// The refused call was created and must not be left running.
	if len(f.finished) != 1 {
		t.Errorf("unwanted call was not ended: %v (calls %d->%d)", f.finished, callsBefore, len(f.calls))
	}
	f.finished = nil
	cfg, _, err := GenerateHash(true)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.VkHashes != "hash2,hash3,hash4,hash6" {
		t.Errorf("replace-oldest hashes = %q", cfg.VkHashes)
	}
	if len(f.finished) != 1 {
		t.Errorf("the dropped call must be ended, finished=%v", f.finished)
	}
}

func TestGenerateRefusedWhileEnvOverrides(t *testing.T) {
	initDB(t)
	(&fakeVK{}).install(t)
	t.Setenv(EnvVkHash, "fixed")
	if _, _, err := GenerateHash(false); err == nil || !strings.Contains(err.Error(), EnvVkHash) {
		t.Errorf("err = %v", err)
	}
}

func TestMaintainHashes(t *testing.T) {
	initDB(t)
	f := &fakeVK{alive: map[string]bool{}}
	f.install(t)

	// Off, no cookies, or auto off: nothing happens.
	if changed, err := MaintainHashes(); changed || err != nil {
		t.Fatalf("disabled: %v %v", changed, err)
	}
	enable(t, nil)
	if changed, _ := MaintainHashes(); changed {
		t.Fatal("no cookies: nothing to do")
	}
	if err := SaveCookies(goodCookies); err != nil {
		t.Fatal(err)
	}

	// Empty list: a hash is created.
	changed, err := MaintainHashes()
	if err != nil || !changed {
		t.Fatalf("empty list: %v %v", changed, err)
	}
	cfg, _ := Load()
	if cfg.VkHashes != "hash1" {
		t.Fatalf("hashes = %q", cfg.VkHashes)
	}

	// Alive: left alone.
	if changed, _ := MaintainHashes(); changed {
		t.Fatal("a live hash was replaced")
	}

	// Unknown (network down): left alone however often it happens.
	f.netDown = true
	for i := 0; i < 3; i++ {
		if changed, _ := MaintainHashes(); changed {
			t.Fatal("hash replaced on a network failure")
		}
	}
	f.netDown = false

	// Dead once: kept (could be a blip); dead twice: replaced in place.
	f.alive["hash1"] = false
	if changed, _ := MaintainHashes(); changed {
		t.Fatal("replaced after a single dead answer")
	}
	changed, err = MaintainHashes()
	if err != nil || !changed {
		t.Fatalf("dead twice: %v %v", changed, err)
	}
	cfg, _ = Load()
	if cfg.VkHashes != "hash2" {
		t.Fatalf("dead hash not replaced in place: %q", cfg.VkHashes)
	}
	if got := CheckHashes(cfg); len(got) != 1 || got[0].Alive == nil || !*got[0].Alive || !got[0].Generated {
		t.Errorf("CheckHashes = %+v", got)
	}

	// Auto off: a dead hash stays.
	enable(t, func(c *Config) { c.AutoVkHash = false; c.VkHashes = "hash2" })
	f.alive["hash2"] = false
	for i := 0; i < 3; i++ {
		if changed, _ := MaintainHashes(); changed {
			t.Fatal("replaced with autoVkHash off")
		}
	}
}

func TestEnsureHashesNow(t *testing.T) {
	initDB(t)
	f := &fakeVK{alive: map[string]bool{}}
	f.install(t)
	enable(t, nil)
	if made, _ := EnsureHashesNow(); made {
		t.Fatal("no cookies, nothing made")
	}
	_ = SaveCookies(goodCookies)
	made, err := EnsureHashesNow()
	if err != nil || !made {
		t.Fatalf("EnsureHashesNow = %v, %v", made, err)
	}
	if made, _ := EnsureHashesNow(); made {
		t.Fatal("a second hash was made while one exists")
	}
}

func TestCheckCookies(t *testing.T) {
	initDB(t)
	f := &fakeVK{alive: map[string]bool{}}
	f.install(t)
	if err := CheckCookies(); err == nil {
		t.Error("no cookies stored must fail")
	}
	_ = SaveCookies(goodCookies)
	if err := CheckCookies(); err != nil {
		t.Errorf("valid cookies: %v", err)
	}
	f.noToken = true
	if err := CheckCookies(); err == nil {
		t.Error("expired cookies must fail")
	}
}
