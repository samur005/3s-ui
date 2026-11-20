package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
)

func inboundIDByTag(t *testing.T, tag string) uint {
	t.Helper()
	var id uint
	if err := database.GetDB().Model(&model.Inbound{}).Select("id").Where("tag = ?", tag).Scan(&id).Error; err != nil || id == 0 {
		t.Fatalf("inbound %q not stored: %v", tag, err)
	}
	return id
}

func TestWireguardInboundsLifecycle(t *testing.T) {
	qwdttDB(t)
	if _, err := (&SettingService{}).GetAllSetting(); err != nil {
		t.Fatal(err)
	}
	alice := mkQwdttClient(t, "alice")
	bob := mkQwdttClient(t, "bob")

	after, err := saveQwdttInbound(t, "new", `{"type":"wg","tag":"wg-main","endpoint":"vpn.example.com"}`, qitoa(alice))
	if err != nil {
		t.Fatal(err)
	}
	if after == nil {
		t.Error("the driver must be told once the save committed")
	}
	// Several of them are fine, of both kinds.
	if _, err := saveQwdttInbound(t, "new", `{"type":"awg","tag":"awg-main","version":"3.0","endpoint":"vpn.example.com"}`, qitoa(alice)+","+qitoa(bob)); err != nil {
		t.Fatal(err)
	}
	if _, err := saveQwdttInbound(t, "new", `{"type":"awg","tag":"awg-2","version":"1.5"}`, ""); err != nil {
		t.Fatal(err)
	}
	wgID, awgID := inboundIDByTag(t, "wg-main"), inboundIDByTag(t, "awg-main")

	// The list shows them with their port, riders and engine; sing-box never gets them.
	list, err := (&InboundService{}).GetAll()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]map[string]any{}
	for _, row := range *list {
		seen[row["tag"].(string)] = row
	}
	if r := seen["wg-main"]; r["type"] != "wg" || r["engine"] != EngineExternal || string(r["listen_port"].(json.RawMessage)) != "51820" {
		t.Errorf("wg row = %v", r)
	}
	if r := seen["awg-main"]; string(r["listen_port"].(json.RawMessage)) != "51821" {
		t.Errorf("awg got the wrong port: %v", r["listen_port"])
	}
	if users, _ := seen["awg-main"]["users"].([]string); len(users) != 2 {
		t.Errorf("awg riders = %v", seen["awg-main"]["users"])
	}
	cfgs, err := (&InboundService{}).GetAllConfig(database.GetDB())
	if err != nil || len(cfgs) != 0 {
		t.Errorf("sing-box was handed %d inbounds (%v)", len(cfgs), err)
	}

	// Alice's configs: a wireguard:// link on the WG inbound, a vpn:// on the AWG one.
	entries, err := (&WgService{}).ClientConfigs(alice)
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries = %+v (%v)", entries, err)
	}
	byTag := map[string]WgClientEntry{}
	for _, e := range entries {
		byTag[e.Tag] = e
	}
	w, a := byTag["wg-main"], byTag["awg-main"]
	if !strings.HasPrefix(w.URI, "wireguard://") || w.Vpn != "" || !strings.Contains(w.Conf, "Endpoint = vpn.example.com:51820") {
		t.Errorf("wg entry = %+v", w)
	}
	if !strings.HasPrefix(a.Vpn, "vpn://") || a.URI != "" || !strings.Contains(a.Conf, "HeaderProtectionKey") || a.Version != "3.0" {
		t.Errorf("awg entry = %+v", a)
	}
	// Different clients get different keys and addresses on the same inbound.
	bobEntries, _ := (&WgService{}).ClientConfigs(bob)
	if len(bobEntries) != 1 || bobEntries[0].Conf == a.Conf {
		t.Errorf("bob = %+v", bobEntries)
	}
	var aliceClient model.Client
	database.GetDB().First(&aliceClient, alice)
	if links := (&WgService{}).ClientLinks(&aliceClient); len(links) != 2 {
		t.Errorf("links = %v", links)
	}

	// An edit that leaves the keys out keeps them, so handed-out configs stay valid.
	before := w.Conf
	if _, err := saveQwdttInbound(t, "edit", `{"id":`+qitoa(wgID)+`,"type":"wg","tag":"wg-main","dns":"9.9.9.9","endpoint":"vpn.example.com"}`, ""); err != nil {
		t.Fatal(err)
	}
	entries, _ = (&WgService{}).ClientConfigs(alice)
	for _, e := range entries {
		if e.Tag == "wg-main" {
			if !strings.Contains(e.Conf, "DNS = 9.9.9.9") {
				t.Error("the edited DNS did not reach the client config")
			}
			if pk(before) != pk(e.Conf) {
				t.Error("an edit rotated the client key")
			}
		}
	}

	// Clashes and bad input are refused with a message.
	if _, err := saveQwdttInbound(t, "new", `{"type":"wg","tag":"dup","listen_port":51820}`, ""); err == nil || !strings.Contains(err.Error(), "51820") {
		t.Errorf("duplicate port: %v", err)
	}
	if _, err := saveQwdttInbound(t, "new", `{"type":"awg","tag":"bad","s1":20,"s2":76}`, ""); err == nil {
		t.Error("S1+56=S2 accepted")
	}
	// The type cannot change in place.
	if _, err := saveQwdttInbound(t, "edit", `{"id":`+qitoa(wgID)+`,"type":"awg","tag":"wg-main"}`, ""); err == nil {
		t.Error("type change accepted")
	}

	// Switching off keeps the row; the driver is told.
	after, err = saveQwdttInbound(t, "edit", `{"id":`+qitoa(awgID)+`,"type":"awg","tag":"awg-main","enable":false}`, "")
	if err != nil || after == nil {
		t.Fatalf("switch off: %v", err)
	}
	entries, _ = (&WgService{}).ClientConfigs(alice)
	for _, e := range entries {
		if e.Tag == "awg-main" && e.Enable {
			t.Error("a switched-off inbound must be flagged")
		}
	}
	if links := (&WgService{}).ClientLinks(&aliceClient); len(links) != 1 {
		t.Errorf("a switched-off inbound must leave the subscription: %v", links)
	}

	// Delete removes the peers with the row.
	if _, err := saveQwdttInbound(t, "del", `"wg-main"`, ""); err != nil {
		t.Fatal(err)
	}
	var n int64
	database.GetDB().Model(&model.WgPeer{}).Where("inbound_id = ?", wgID).Count(&n)
	if n != 0 {
		t.Errorf("%d peer rows left behind", n)
	}
}

// pk pulls the client private key line out of a config.
func pk(conf string) string {
	for _, l := range strings.Split(conf, "\n") {
		if strings.HasPrefix(l, "PrivateKey") {
			return l
		}
	}
	return ""
}

func TestWgGenerateOffersEveryVersion(t *testing.T) {
	for _, v := range []string{"1.5", "2.0", "2.5", "3.0", "3.1"} {
		if _, err := (&WgService{}).GenerateObfuscation(v); err != nil {
			t.Errorf("%s: %v", v, err)
		}
	}
	if _, err := (&WgService{}).GenerateObfuscation("9"); err == nil {
		t.Error("unknown version accepted")
	}
}
