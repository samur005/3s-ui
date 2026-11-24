package service

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
)

func TestValidateSubProfileSetting(t *testing.T) {
	ok := map[string]string{
		"subSupportUrl":          "https://t.me/support",
		"subProfileUrl":          "https://example.com/u/{{SUB_ID}}",
		"subProfileMode":         "builtin",
		"subHappTunType":         "gvisor",
		"subHappPerAppMode":      "bypass",
		"subRoutingRules":        "https://example.com/routing.json",
		"subAnnounce":            "line one\nline two",
		"subHappAutoConnectType": "lastused",
		"subTitle":               "Мой VPN",
	}
	for k, v := range ok {
		if err := ValidateSubProfileSetting(k, v); err != nil {
			t.Errorf("%s=%q: %v", k, v, err)
		}
	}
	bad := map[string]string{
		"subSupportUrl":       "javascript:alert(1)",
		"subProfileMode":      "weird",
		"subHappNewUrl":       "ftp://x",
		"subHappProviderId":   "a\r\nSet-Cookie: x",
		"subRoutingRules":     `{"broken":`,
		"subIncyRoutingRules": "https://a\nhttps://b",
	}
	for k, v := range bad {
		if err := ValidateSubProfileSetting(k, v); err == nil {
			t.Errorf("%s=%q accepted", k, v)
		}
	}
	if err := ValidateSubProfileSetting("someOtherKey", "x\ny"); err != nil {
		t.Errorf("unrelated keys are not ours to validate: %v", err)
	}
}

func TestNormalizeHappRouting(t *testing.T) {
	out, err := NormalizeHappRouting(`{ "Name": "ru", "DirectSites": ["geosite:ru"] }`, false)
	if err != nil || !strings.HasPrefix(out, "happ://routing/onadd/") {
		t.Fatalf("json -> %q %v", out, err)
	}
	dec, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(out, "happ://routing/onadd/"))
	if err != nil || !strings.Contains(string(dec), `"Name":"ru"`) || strings.Contains(string(dec), " ") {
		t.Fatalf("payload %q %v", dec, err)
	}
	if again, err := NormalizeHappRouting(out, false); err != nil || again != out {
		t.Fatalf("a happ:// link must pass through: %q %v", again, err)
	}
	if _, err := NormalizeHappRouting("happ://routing/onadd/!!!", false); err == nil {
		t.Fatal("garbage deeplink accepted")
	}
	if _, err := NormalizeHappRouting("https://example.com/r.json", false); err == nil {
		t.Fatal("a remote source answering with another URL accepted")
	}
	if _, err := NormalizeHappRouting("https://example.com/r.json", true); !errors.Is(err, errRoutingRemote) {
		t.Fatalf("remote: %v", err)
	}
	if _, err := NormalizeHappRouting("http://example.com/r.json", true); err == nil || errors.Is(err, errRoutingRemote) {
		t.Fatal("plain http routing source accepted")
	}
	if _, err := NormalizeHappRouting(`{"a":"`+strings.Repeat("x", 9000)+`"}`, false); err == nil {
		t.Fatal("oversized routing accepted")
	}
}

func TestResolveHappRoutingCachesAndServesStale(t *testing.T) {
	calls := 0
	fail := false
	prev := routingFetch
	routingFetch = func(src string) (string, error) {
		calls++
		if fail {
			return "", errors.New("down")
		}
		return "happ://routing/onadd/e30=", nil
	}
	t.Cleanup(func() {
		routingFetch = prev
		routingCacheMu.Lock()
		routingCache = map[string]*routingCacheEntry{}
		routingCacheMu.Unlock()
	})
	src := "https://routing.example/r.json"
	if got := ResolveHappRouting(src); got != "happ://routing/onadd/e30=" {
		t.Fatalf("first = %q", got)
	}
	ResolveHappRouting(src)
	if calls != 1 {
		t.Fatalf("not cached: %d fetches", calls)
	}
	routingCacheMu.Lock()
	routingCache[src].fetchedAt = time.Now().Add(-time.Hour)
	routingCacheMu.Unlock()
	fail = true
	if got := ResolveHappRouting(src); got != "happ://routing/onadd/e30=" {
		t.Fatalf("stale not served: %q", got)
	}
	ResolveHappRouting(src) // inside the retry window: no new fetch
	if calls != 2 {
		t.Fatalf("retry window ignored: %d fetches", calls)
	}
	if ResolveHappRouting("") != "" || ResolveHappRouting(`{"bad"`) != "" {
		t.Fatal("empty / invalid must give no header")
	}
}

func TestIncyRoutingLine(t *testing.T) {
	cases := map[string]string{
		"":                             "",
		"https://r.example/incy.json":  "incy://autorouting/onadd/https://r.example/incy.json",
		"incy://autorouting/onadd/abc": "incy://autorouting/onadd/abc",
		"ftp://nope":                   "",
	}
	for in, want := range cases {
		if got := IncyRoutingLine(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestEnforceHwidLimit(t *testing.T) {
	setupMasterForTest(t)
	c := model.Client{Name: "dev", Enable: true, LimitHwid: 2}
	database.GetDB().Create(&c)
	var cs ClientService

	r, err := cs.EnforceHwid(&c, HwidRequest{})
	if err != nil || !r.Allowed || !r.Active || !r.NotSupported {
		t.Fatalf("no hwid: %+v %v", r, err)
	}
	if r, _ := cs.EnforceHwid(&c, HwidRequest{Hwid: "abc"}); !r.NotSupported {
		t.Fatal("a too-short hwid must count as missing")
	}
	r, _ = cs.EnforceHwid(&c, HwidRequest{Hwid: "device-one", DeviceOS: "iOS"})
	if !r.Allowed || r.Registered != 1 || r.LimitReached {
		t.Fatalf("first device %+v", r)
	}
	r, _ = cs.EnforceHwid(&c, HwidRequest{Hwid: "device-two"})
	if !r.Allowed || r.Registered != 2 || !r.LimitReached {
		t.Fatalf("second device %+v", r)
	}
	r, _ = cs.EnforceHwid(&c, HwidRequest{Hwid: "device-three"})
	if r.Allowed || !r.MaxDevicesReached {
		t.Fatalf("third device let in: %+v", r)
	}
	r, _ = cs.EnforceHwid(&c, HwidRequest{Hwid: "device-one"})
	if !r.Allowed {
		t.Fatal("a known device was refused")
	}
	list, _ := cs.ListHwids(c.Id)
	if len(list) != 2 {
		t.Fatalf("list %d", len(list))
	}
	for _, d := range list {
		if strings.Contains(d.HwidHash, "device") {
			t.Fatal("raw hwid stored")
		}
	}
	if err := cs.DeleteHwids(c.Id, list[0].Id); err != nil {
		t.Fatal(err)
	}
	if r, _ := cs.EnforceHwid(&c, HwidRequest{Hwid: "device-three"}); !r.Allowed {
		t.Fatal("a freed slot was not reusable")
	}
	if err := cs.DeleteHwids(c.Id, 0); err != nil {
		t.Fatal(err)
	}
	if list, _ := cs.ListHwids(c.Id); len(list) != 0 {
		t.Fatal("reset all left devices")
	}
	unlimited := model.Client{Name: "free"}
	if r, _ := cs.EnforceHwid(&unlimited, HwidRequest{Hwid: "whatever-1"}); !r.Allowed || r.Active {
		t.Fatalf("unlimited client %+v", r)
	}
}
