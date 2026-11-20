package service

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
	"github.com/samur005/3s-ui/qwdtt"
)

func qwdttDB(t *testing.T) {
	t.Helper()
	t.Setenv(qwdtt.EnvVkHash, "")
	dir := t.TempDir()
	t.Setenv("S3UI_DB_FOLDER", dir)
	if err := database.InitDB(filepath.Join(dir, "test.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.CloseDBForTest() })
}

// With qWDTT on and routed through the core, the core config carries the TUN
// inbound and the panel's route rules; with it off the config is untouched.
func TestGetConfigInjectsQwdttTun(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("qWDTT routing is Linux only")
	}
	qwdttDB(t)
	build := func() (tags []string, firstAction string) {
		raw, err := (&ConfigService{}).GetConfig("")
		if err != nil {
			t.Fatal(err)
		}
		var cfg struct {
			Inbounds []struct {
				Tag string `json:"tag"`
			} `json:"inbounds"`
			Route struct {
				Rules []map[string]any `json:"rules"`
			} `json:"route"`
		}
		if err := json.Unmarshal(*raw, &cfg); err != nil {
			t.Fatal(err)
		}
		for _, in := range cfg.Inbounds {
			tags = append(tags, in.Tag)
		}
		if len(cfg.Route.Rules) > 0 {
			firstAction, _ = cfg.Route.Rules[0]["action"].(string)
		}
		return
	}

	tags, _ := build()
	for _, tag := range tags {
		if tag == qwdtt.TunTag {
			t.Fatal("TUN injected while qWDTT is off")
		}
	}

	c := qwdtt.Default()
	c.Enabled, c.OutboundTag = true, "direct"
	if _, err := qwdtt.Save(c); err != nil {
		t.Fatal(err)
	}
	tags, first := build()
	if !strings.Contains(strings.Join(tags, ","), qwdtt.TunTag) {
		t.Errorf("TUN inbound missing from %v", tags)
	}
	if first != "sniff" {
		t.Errorf("first route rule = %q, want the panel's sniff rule", first)
	}
	if qwdtt.CoreNeedsRestart(c) {
		t.Error("the core was just built with the fragment")
	}
}

func TestQwdttServiceSaveAndLink(t *testing.T) {
	qwdttDB(t)
	var svc QwdttService
	alice := &model.Client{Name: "alice", Enable: true, Inbounds: json.RawMessage(`[]`)}

	if err := svc.Save(`{"workers": 999}`); err == nil {
		t.Error("an invalid config was accepted")
	}
	if got := svc.ClientLink(alice); got != "" {
		t.Errorf("link while disabled: %q", got)
	}

	// Partial JSON keeps what it does not mention.
	if err := svc.Save(`{"enabled": false, "remark": "Home", "subHost": "1.2.3.4:56000", "vkHashes": "h1"}`); err != nil {
		t.Fatal(err)
	}
	cfg, _ := qwdtt.Load()
	if !cfg.AutoVkHash || !cfg.RouteThroughCore || cfg.Remark != "Home" {
		t.Errorf("partial save lost defaults: %+v", cfg)
	}
	if err := svc.Save(`{"enabled": true, "routeThroughCore": false}`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(qwdtt.Get().Shutdown)
	if got := svc.ClientLink(alice); got != "" {
		t.Errorf("a client that does not ride the inbound got a link: %q", got)
	}
	alice.Inbounds = json.RawMessage(`[` + strconv.Itoa(int(qwdtt.InboundID())) + `]`)
	link := svc.ClientLink(alice)
	if !strings.HasPrefix(link, "qwdtt://config?") || !strings.Contains(link, "peer=1.2.3.4%3A56000") {
		t.Errorf("link = %q", link)
	}

	v, err := svc.View()
	if err != nil {
		t.Fatal(err)
	}
	if v.Link != link || v.ResolvedHost != "1.2.3.4:56000" || len(v.Hashes) != 1 || v.HashSource != "manual" {
		t.Errorf("view = %+v", v)
	}
	doc, err := svc.SubscriptionJSON()
	if err != nil || !strings.Contains(doc, `"subscriptionName": "Home"`) {
		t.Errorf("subscription = %q, %v", doc, err)
	}

	t.Setenv(qwdtt.EnvVkHash, "fromenv")
	if v, _ := svc.View(); v.HashSource != "env" || strings.Join(v.Hashes, ",") != "fromenv" {
		t.Errorf("env override not reflected: %+v", v)
	}
}
