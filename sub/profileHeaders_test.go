package sub

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
	"github.com/samur005/3s-ui/service"
)

func TestProfileHeadersCommonSet(t *testing.T) {
	client := &model.Client{Id: 7, Name: "alice", Remark: "Alice", TgId: 42}
	cfg := service.SubProfileConfig{
		Title:         "VPN для {{USERNAME}}",
		SupportURL:    "https://t.me/help?u={{TELEGRAM_ID}}",
		ProfileMode:   service.SubProfileModeCustom,
		ProfileURL:    "https://example.com/p/{{SUB_ID}}",
		Announce:      "Привет, {{REMARK}}!",
		EnableRouting: true,
		RoutingRules:  `{"Name":"x"}`,
		HideSettings:  true,
	}
	h := buildProfileHeaders(profileContext{cfg: cfg, client: client, userAgent: "v2rayN/7"})
	title := h.Get("Profile-Title")
	if !strings.HasPrefix(title, "base64:") {
		t.Fatalf("non-ASCII title not base64: %q", title)
	}
	if dec, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(title, "base64:")); string(dec) != "VPN для alice" {
		t.Fatalf("title %q", dec)
	}
	if h.Get("Support-Url") != "https://t.me/help?u=42" {
		t.Errorf("Support-Url %q", h.Get("Support-Url"))
	}
	if h.Get("Profile-Web-Page-Url") != "https://example.com/p/alice" {
		t.Errorf("Profile-Web-Page-Url %q", h.Get("Profile-Web-Page-Url"))
	}
	ann := h.Get("Announce")
	if dec, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(ann, "base64:")); !strings.HasPrefix(ann, "base64:") || string(dec) != "Привет, Alice!" {
		t.Errorf("Announce %q", ann)
	}
	if h.Get("Routing-Enable") != "true" || !strings.HasPrefix(h.Get("Routing"), "happ://routing/onadd/") || h.Get("Hide-Settings") != "1" {
		t.Errorf("routing headers %v", h)
	}
	for _, k := range []string{"ProviderID", "Tun-Mode", "Sub-Expire"} {
		if h.Get(k) != "" {
			t.Errorf("Happ header %s sent without Happ autodetect", k)
		}
	}
	cfg.Title, cfg.ProfileMode = "My VPN", service.SubProfileModeBuiltin
	h = buildProfileHeaders(profileContext{cfg: cfg, client: client, builtinURL: "https://sub.example/s/alice?format=page"})
	if h.Get("Profile-Title") != "My VPN" || h.Get("Profile-Web-Page-Url") != "https://sub.example/s/alice?format=page" {
		t.Errorf("plain title / builtin page: %v", h)
	}
	if got := buildProfileHeaders(profileContext{client: client}); len(got) != 0 {
		t.Errorf("empty settings must add nothing, got %v", got)
	}
}

func fullHapp() service.HappConfig {
	return service.HappConfig{
		AutoDetect: true, ProviderID: "prov1", NewURL: "https://new.example/s", FallbackURL: "https://fb.example/s",
		SubInfoColor: "warning", SubInfoText: "Pay soon", SubInfoButtonText: "Pay", SubInfoButtonLink: "https://pay.example",
		SubExpire: true, SubExpireButtonLink: "https://renew.example", NotificationExpire: true, NoLimit: true,
		AlwaysHwid: true, TunMode: "1", TunType: "gvisor", ExcludeRoutes: "10.0.0.0/8", ExcludeApns: true,
		ColorProfile: "dark", PingType: "http", AutoConnect: true, AutoConnectType: "fastest",
		PerAppMode: "exclude", PerAppList: "com.bank", LocalProxyAuth: "auto",
	}
}

func TestHappHeadersOnlyForHapp(t *testing.T) {
	cfg := service.SubProfileConfig{Happ: fullHapp()}
	client := &model.Client{Name: "bob"}
	h := buildProfileHeaders(profileContext{cfg: cfg, client: client, userAgent: "Happ/3.2.1"})
	want := map[string]string{
		"ProviderID": "prov1", "New-Url": "https://new.example/s", "Fallback-Url": "https://fb.example/s",
		"Sub-Info-Color": "red", "Sub-Info-Text": "Pay soon", "Sub-Info-Button-Text": "Pay",
		"Sub-Info-Button-Link": "https://pay.example", "Sub-Expire": "1", "Sub-Expire-Button-Link": "https://renew.example",
		"Notification-Subs-Expire": "1", "No-Limit-Enabled": "1", "Subscription-Always-Hwid-Enable": "1",
		"Tun-Mode": "1", "Tun-Type": "gvisor", "Exclude-Routes": "10.0.0.0/8", "Exclude-Apns-Enable": "true",
		"Color-Profile": "dark", "Ping-Type": "proxy", "Subscription-Autoconnect": "1",
		"Subscription-Autoconnect-Type": "lowestdelay", "Socks-Auth-Mode": "auto", "Http-Auth-Mode": "auto",
		"Per-App-Proxy-Mode": "bypass", "Per-App-Proxy-List": "com.bank",
		// Happ-managed client without the routing / hide toggles: explicit "0".
		"Routing-Enable": "0", "Hide-Settings": "0",
	}
	for k, v := range want {
		if got := h.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	for _, ua := range []string{"v2rayNG/1.9", "Hiddify", "happy-client/1", "clash-verge"} {
		if got := buildProfileHeaders(profileContext{cfg: cfg, client: client, userAgent: ua}); len(got) != 0 {
			t.Errorf("UA %q got Happ headers %v", ua, got)
		}
	}
	cfg.Happ.AutoDetect = false
	if got := buildProfileHeaders(profileContext{cfg: cfg, client: client, userAgent: "Happ/3.2.1"}); len(got) != 0 {
		t.Errorf("autodetect off still sent %v", got)
	}
}

func setSettings(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		if err := database.GetDB().Create(&model.Setting{Key: k, Value: v}).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func hwidClient(t *testing.T, limit int) {
	t.Helper()
	c := model.Client{
		Name: "hw", Enable: true, LimitHwid: limit, Config: json.RawMessage(`{}`), Inbounds: json.RawMessage(`[]`),
		Links: json.RawMessage(`[{"type":"external","remark":"r","uri":"vless://11111111-1111-1111-1111-111111111111@h.example:443#r"}]`),
	}
	if err := database.GetDB().Create(&c).Error; err != nil {
		t.Fatal(err)
	}
}

func get(t *testing.T, url string, hdr map[string]string) (int, http.Header, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(b)
}

// End to end through the real sub router: the headers on links / JSON /
// Clash, the Incy line in the body, and the device limit.
func TestSubscriptionServesProfileHeadersAndHwidGate(t *testing.T) {
	antiProbingDB(t)
	setSettings(t, map[string]string{
		"subTitle": "Acme {{NAME}}", "subSupportUrl": "https://help.example", "subAnnounce": "hi",
		"subHideSettings": "true", "subHappAutoDetect": "true", "subHappProviderId": "prov",
		"subIncyEnableRouting": "true", "subIncyRoutingRules": "https://r.example/incy.json", "subEncode": "false",
	})
	hwidClient(t, 1)
	engine, subPath := antiProbingSubEngine(t)
	srv := httptest.NewServer(engine)
	defer srv.Close()
	base := srv.URL + subPath + "hw"

	for _, format := range []string{"", "?format=json", "?format=clash"} {
		code, h, _ := get(t, base+format, map[string]string{"X-HWID": "phone-123456", "User-Agent": "Happ/3.2"})
		if code != 200 {
			t.Fatalf("%q: status %d", format, code)
		}
		if h.Get("Profile-Title") != "Acme hw" || h.Get("Support-Url") != "https://help.example" ||
			h.Get("Announce") == "" || h.Get("Hide-Settings") != "1" || h.Get("ProviderID") != "prov" ||
			h.Get("X-Hwid-Active") != "true" || h.Get("Subscription-Userinfo") == "" {
			t.Errorf("%q: headers %v", format, h)
		}
		if cd := h.Get("Content-Disposition"); cd != "" && !strings.Contains(cd, "Acme hw") {
			t.Errorf("%q: Content-Disposition %q not renamed", format, cd)
		}
	}
	_, _, body := get(t, base, map[string]string{"X-HWID": "phone-123456"})
	if !strings.Contains(body, "incy://autorouting/onadd/https://r.example/incy.json") {
		t.Errorf("Incy routing line missing from the body:\n%s", body)
	}
	code, h, _ := get(t, base, map[string]string{"X-HWID": "laptop-654321"})
	if code != http.StatusNotFound || h.Get("X-Hwid-Max-Devices-Reached") != "true" || h.Get("X-Hwid-Limit") != "true" {
		t.Fatalf("second device: %d %v", code, h)
	}
	code, h, _ = get(t, base, nil)
	if code != 200 || h.Get("X-Hwid-Not-Supported") != "true" {
		t.Fatalf("no hwid: %d %v", code, h)
	}
	if code, _, _ := get(t, base+"?format=info", map[string]string{"X-HWID": "laptop-654321"}); code != 200 {
		t.Fatalf("info refused: %d", code)
	}
}
