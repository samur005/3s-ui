package service

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
	"github.com/samur005/3s-ui/qwdtt"

	"gorm.io/gorm"
)

func saveQwdttInbound(t *testing.T, act, data, initUsers string) (func(), error) {
	t.Helper()
	var after func()
	err := database.GetDB().Transaction(func(tx *gorm.DB) error {
		var err error
		after, err = (&InboundService{}).Save(tx, act, json.RawMessage(data), initUsers, "panel.example.com")
		return err
	})
	return after, err
}

func mkQwdttClient(t *testing.T, name string) uint {
	t.Helper()
	c := model.Client{Name: name, Enable: true, Config: json.RawMessage(`{}`), Inbounds: json.RawMessage(`[]`), Links: json.RawMessage(`[]`)}
	if err := database.GetDB().Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	return c.Id
}

func TestQwdttInboundLifecycle(t *testing.T) {
	qwdttDB(t)
	if _, err := (&SettingService{}).GetAllSetting(); err != nil {
		t.Fatal(err)
	}
	alice := mkQwdttClient(t, "alice")
	mkQwdttClient(t, "bob")

	after, err := saveQwdttInbound(t, "new",
		`{"type":"qwdtt","tag":"qwdtt","listenAddr":"0.0.0.0:57000","subHost":"1.2.3.4:57000","junk":"x"}`,
		strings.Join([]string{qitoa(alice)}, ","))
	if err != nil {
		t.Fatal(err)
	}
	if after == nil {
		t.Error("the driver must be told once the save committed")
	}
	id := qwdtt.InboundID()
	if id == 0 {
		t.Fatal("inbound not stored")
	}
	cfg, _ := qwdtt.Load()
	if !cfg.Enabled || cfg.DTLSPort() != 57000 || len(cfg.Password) != 16 {
		t.Errorf("cfg = %+v", cfg)
	}

	// The list shows it like any inbound: port, and the clients riding it.
	list, err := (&InboundService{}).GetAll()
	if err != nil {
		t.Fatal(err)
	}
	row := (*list)[0]
	if row["type"] != "qwdtt" || string(row["listen_port"].(json.RawMessage)) != "57000" {
		t.Errorf("list row = %v", row)
	}
	if users, ok := row["users"].([]string); !ok || len(users) != 1 || users[0] != "alice" {
		t.Errorf("riders = %v", row["users"])
	}
	if got := (&QwdttService{}).Riders(); len(got) != 1 || got[0] != "alice" {
		t.Errorf("Riders = %v", got)
	}

	// sing-box must never be handed it.
	cfgs, err := (&InboundService{}).GetAllConfig(database.GetDB())
	if err != nil || len(cfgs) != 0 {
		t.Errorf("GetAllConfig = %d entries, %v: qwdtt leaked into the core config", len(cfgs), err)
	}

	// One server per host.
	if _, err := saveQwdttInbound(t, "new", `{"type":"qwdtt","tag":"qwdtt2"}`, ""); err == nil ||
		!strings.Contains(err.Error(), "only one qwdtt inbound") {
		t.Errorf("second inbound: %v", err)
	}

	// Edit keeps the switch when the form does not say, and switches it off.
	if _, err := saveQwdttInbound(t, "edit", `{"id":`+qitoa(id)+`,"type":"qwdtt","tag":"qwdtt","listenAddr":"0.0.0.0:57000"}`, ""); err != nil {
		t.Fatal(err)
	}
	if c, _ := qwdtt.Load(); !c.Enabled || c.Password != cfg.Password {
		t.Errorf("a plain edit changed the switch or password: %+v", c)
	}
	if _, err := saveQwdttInbound(t, "edit", `{"id":`+qitoa(id)+`,"type":"qwdtt","tag":"qwdtt","enable":false,"listenAddr":"0.0.0.0:57000"}`, ""); err != nil {
		t.Fatal(err)
	}
	if c, _ := qwdtt.Load(); c.Enabled {
		t.Error("switching the inbound off must disable qWDTT")
	}

	// A bad config is refused and nothing changes.
	if _, err := saveQwdttInbound(t, "edit", `{"id":`+qitoa(id)+`,"type":"qwdtt","tag":"qwdtt","listenAddr":"nope"}`, ""); err == nil {
		t.Error("invalid listen address accepted")
	}

	// The type cannot move between sing-box and external in place.
	if _, err := saveQwdttInbound(t, "edit", `{"id":`+qitoa(id)+`,"type":"direct","tag":"qwdtt","listen":"::","listen_port":5000}`, ""); err == nil ||
		!strings.Contains(err.Error(), "cannot be changed") {
		t.Errorf("type change: %v", err)
	}

	// Delete removes the row and unbinds the client.
	after, err = saveQwdttInbound(t, "del", `"qwdtt"`, "")
	if err != nil {
		t.Fatal(err)
	}
	if after == nil {
		t.Error("deleting must tell the driver to stop the server")
	}
	if qwdtt.InboundID() != 0 {
		t.Error("inbound still stored after delete")
	}
	var c model.Client
	database.GetDB().First(&c, alice)
	if strings.TrimSpace(string(c.Inbounds)) != "[]" && !strings.Contains(string(c.Inbounds), "[]") {
		t.Errorf("client still rides the deleted inbound: %s", c.Inbounds)
	}
}

func TestQwdttMigrationFromTheOldPage(t *testing.T) {
	qwdttDB(t)
	if _, err := (&SettingService{}).GetAllSetting(); err != nil {
		t.Fatal(err)
	}
	mkQwdttClient(t, "alice")
	bob := mkQwdttClient(t, "bob")
	svc := &QwdttService{}

	// Nothing stored: nothing created.
	svc.MigrateToInbound()
	if qwdtt.InboundID() != 0 {
		t.Fatal("a fresh install must not get an inbound")
	}

	db := database.GetDB()
	legacy := `{"enabled":true,"password":"oldpw","subHost":"1.2.3.4:56000","vkHashes":"h1","accessMode":"selected","clients":["alice"]}`
	if err := db.Exec("INSERT INTO qwdtt_states (id, config, vk_cookies, vk_calls) VALUES (1, ?, 'ck', '{}')", legacy).Error; err != nil {
		t.Fatal(err)
	}
	svc.MigrateToInbound()
	id := qwdtt.InboundID()
	if id == 0 {
		t.Fatal("the old configuration was not turned into an inbound")
	}
	cfg, _ := qwdtt.Load()
	if !cfg.Enabled || cfg.Password != "oldpw" || cfg.VkHashes != "h1" {
		t.Errorf("migrated cfg = %+v", cfg)
	}
	if r := svc.Riders(); len(r) != 1 || r[0] != "alice" {
		t.Errorf("selected access must become bindings: %v", r)
	}
	_ = bob
	// The cookies stay, the old copy is gone, and a second run changes nothing.
	if !qwdtt.HasCookies() {
		t.Error("migration lost the VK cookies")
	}
	if _, _, _, ok, _ := qwdtt.LegacyConfig(); ok {
		t.Error("the old copy must be cleared")
	}
	svc.MigrateToInbound()
	var n int64
	db.Model(model.Inbound{}).Where("type = ?", "qwdtt").Count(&n)
	if n != 1 {
		t.Errorf("%d qwdtt inbounds after a second run", n)
	}

	// Deleting the inbound is not undone by the next start.
	if _, err := saveQwdttInbound(t, "del", `"qwdtt"`, ""); err != nil {
		t.Fatal(err)
	}
	svc.MigrateToInbound()
	if qwdtt.InboundID() != 0 {
		t.Error("a deleted inbound came back")
	}
}

func TestQwdttMigrationAccessAllBindsEveryone(t *testing.T) {
	qwdttDB(t)
	if _, err := (&SettingService{}).GetAllSetting(); err != nil {
		t.Fatal(err)
	}
	mkQwdttClient(t, "alice")
	mkQwdttClient(t, "bob")
	if err := database.GetDB().Exec("INSERT INTO qwdtt_states (id, config) VALUES (1, ?)", `{"enabled":false,"password":"p"}`).Error; err != nil {
		t.Fatal(err)
	}
	svc := &QwdttService{}
	svc.MigrateToInbound()
	if r := svc.Riders(); len(r) != 2 {
		t.Errorf("mode all must bind every client: %v", r)
	}
	if c, _ := qwdtt.Load(); c.Enabled {
		t.Error("a disabled old setup must stay disabled")
	}
}

func qitoa(v uint) string { return strconv.FormatUint(uint64(v), 10) }
