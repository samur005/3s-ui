package qwdtt

import (
	"encoding/json"
	"testing"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
)

func TestLoadFreshInstallIsDisabledWithDefaults(t *testing.T) {
	initDB(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Enabled || c.Password != "" || c.ListenAddr != "0.0.0.0:56000" {
		t.Errorf("fresh config = %+v", c)
	}
}

func TestSaveRoundTripGeneratesPassword(t *testing.T) {
	initDB(t)
	in := Default()
	in.Enabled, in.Remark = true, "Home"
	in.VkHashes = "https://vk.ru/call/join/h1, h2"
	saved, err := Save(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Password) != 16 {
		t.Errorf("password not generated: %q", saved.Password)
	}
	got, _ := Load()
	if got.Password != saved.Password || got.Remark != "Home" || got.VkHashes != "h1,h2" {
		t.Errorf("round trip = %+v", got)
	}
	if !got.Enabled {
		t.Error("the inbound's switch is the enabled flag")
	}
	// A second save keeps the password.
	again, _ := Save(got)
	if again.Password != saved.Password {
		t.Error("password changed on re-save")
	}
}

func TestSaveRejectsInvalidAndStoresNothing(t *testing.T) {
	initDB(t)
	bad := Default()
	bad.Workers = 500
	if _, err := Save(bad); err == nil {
		t.Fatal("invalid config saved")
	}
	if c, _ := Load(); c.Workers != 16 {
		t.Errorf("rejected save changed the stored config: %+v", c)
	}
}

func TestRowsWithoutNewKeysKeepTheirDefaults(t *testing.T) {
	// A row written before autoVkHash/routeThroughCore existed.
	c := decodeConfig(`{"enabled":true,"password":"p"}`)
	if !c.AutoVkHash || !c.RouteThroughCore || !c.Enabled {
		t.Errorf("decoded = %+v", c)
	}
	c = decodeConfig(`{"autoVkHash":false,"routeThroughCore":false}`)
	if c.AutoVkHash || c.RouteThroughCore {
		t.Errorf("explicit false was overwritten: %+v", c)
	}
	if c := decodeConfig("not json"); c.ListenAddr != "0.0.0.0:56000" {
		t.Errorf("garbage must decode to defaults: %+v", c)
	}
}

func TestStateSurvivesCookieAndConfigWritesIndependently(t *testing.T) {
	initDB(t)
	enable(t, func(c *Config) { c.Remark = "keep" })
	if err := SaveCookies(goodCookies); err != nil {
		t.Fatal(err)
	}
	if c, _ := Load(); c.Remark != "keep" {
		t.Error("saving cookies clobbered the config")
	}
	enable(t, func(c *Config) { c.Remark = "again" })
	if !HasCookies() {
		t.Error("saving the config clobbered the cookies")
	}
}

func TestSaveCreatesTheInboundRowOnce(t *testing.T) {
	initDB(t)
	if InboundID() != 0 {
		t.Fatal("fresh install must have no qwdtt inbound")
	}
	in := Default()
	in.Enabled = true
	if _, err := Save(in); err != nil {
		t.Fatal(err)
	}
	id := InboundID()
	if id == 0 {
		t.Fatal("Save did not create the inbound")
	}
	if _, err := Save(in); err != nil {
		t.Fatal(err)
	}
	var n int64
	database.GetDB().Model(model.Inbound{}).Where("type = ?", InboundType).Count(&n)
	if n != 1 || InboundID() != id {
		t.Errorf("rows = %d, id %d -> %d", n, id, InboundID())
	}
	row, _ := LoadInbound(database.GetDB())
	var opts map[string]any
	if err := json.Unmarshal(row.Options, &opts); err != nil {
		t.Fatal(err)
	}
	if _, has := opts["enabled"]; has {
		t.Error("enabled must live in the row's switch, not in the options")
	}
	if opts["listen_port"] != float64(56000) || opts["listen"] != "0.0.0.0" {
		t.Errorf("the list reads listen/listen_port from the options: %v", opts)
	}
}

func TestSwitchingTheInboundOffDisablesQwdtt(t *testing.T) {
	initDB(t)
	in := Default()
	in.Enabled = true
	if _, err := Save(in); err != nil {
		t.Fatal(err)
	}
	database.GetDB().Model(model.Inbound{}).Where("type = ?", InboundType).Update("enable", false)
	if c, _ := Load(); c.Enabled {
		t.Error("a disabled inbound must read as disabled")
	}
}

func TestOnlyOneInbound(t *testing.T) {
	initDB(t)
	if _, err := Save(Default()); err != nil {
		t.Fatal(err)
	}
	other, err := OtherInboundExists(database.GetDB(), 0)
	if err != nil || !other {
		t.Errorf("an existing inbound must count as another one for a new row: %v %v", other, err)
	}
	if other, _ := OtherInboundExists(database.GetDB(), InboundID()); other {
		t.Error("the inbound is not another one than itself")
	}
}

func TestNormalizeOptions(t *testing.T) {
	opts, cfg, err := NormalizeOptions(json.RawMessage(`{"listenAddr":"0.0.0.0:57000","vkHashes":"https://vk.ru/call/join/a, b","junk":1,"workers":8}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.VkHashes != "a,b" || cfg.Workers != 8 || len(cfg.Password) != 16 {
		t.Errorf("cfg = %+v", cfg)
	}
	var m map[string]any
	_ = json.Unmarshal(opts, &m)
	if _, has := m["junk"]; has {
		t.Error("unknown keys must not stay in the row")
	}
	if m["listen_port"] != float64(57000) {
		t.Errorf("listen_port = %v", m["listen_port"])
	}
	if _, _, err := NormalizeOptions(json.RawMessage(`{"listenAddr":"nonsense"}`), nil); err == nil {
		t.Error("a bad listen address must be refused")
	}
}

func TestLegacyConfigAndClear(t *testing.T) {
	initDB(t)
	if _, _, _, ok, _ := LegacyConfig(); ok {
		t.Fatal("no legacy config on a fresh install")
	}
	row, _ := loadRow(database.GetDB())
	row.Config = `{"enabled":true,"password":"pw","accessMode":"selected","clients":["bob"]}`
	if err := saveRow(database.GetDB(), row); err != nil {
		t.Fatal(err)
	}
	cfg, mode, clients, ok, err := LegacyConfig()
	if err != nil || !ok || !cfg.Enabled || cfg.Password != "pw" || mode != "selected" || len(clients) != 1 {
		t.Fatalf("legacy = %+v %q %v %v %v", cfg, mode, clients, ok, err)
	}
	if err := ClearLegacyConfig(); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok, _ := LegacyConfig(); ok {
		t.Error("cleared legacy config must be gone")
	}
}

func TestNormalizeKeepsWhatTheFormLeavesOut(t *testing.T) {
	stored := json.RawMessage(`{"password":"keepme","vkHashes":"h1","autoVkHash":false,"workers":4}`)
	_, cfg, err := NormalizeOptions(json.RawMessage(`{"dns":"1.1.1.1"}`), stored)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Password != "keepme" || cfg.VkHashes != "h1" || cfg.AutoVkHash || cfg.Workers != 4 || cfg.DNS != "1.1.1.1" {
		t.Errorf("cfg = %+v", cfg)
	}
	if _, _, err := NormalizeOptions(json.RawMessage(`{"workers":"many"}`), stored); err == nil {
		t.Error("a wrong type must be refused, not ignored")
	}
}
