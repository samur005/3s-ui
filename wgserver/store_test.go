package wgserver

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/samur005/3s-ui/database/model"
	"gorm.io/gorm"
)

func addInbound(t *testing.T, db *gorm.DB, typ, tag, options string, enable bool) uint {
	t.Helper()
	norm, err := NormalizeOptions(db, typ, 0, json.RawMessage(options), nil)
	if err != nil {
		t.Fatalf("normalize %s: %v", tag, err)
	}
	row := model.Inbound{Type: typ, Tag: tag, Options: norm, Addrs: json.RawMessage("null"), OutJson: json.RawMessage("null")}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if !enable {
		db.Model(&row).Update("enable", false)
	}
	return row.Id
}

func addClient(t *testing.T, db *gorm.DB, name string, enable bool, inbounds ...uint) uint {
	t.Helper()
	ib, _ := json.Marshal(inbounds)
	c := model.Client{Name: name, Enable: true, Config: json.RawMessage(`{}`), Inbounds: ib, Links: json.RawMessage(`[]`)}
	if err := db.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	if !enable {
		db.Model(&c).Update("enable", false)
	}
	return c.Id
}

func TestNormalizeFillsEverythingForANewInbound(t *testing.T) {
	initDB(t)
	db := DB()
	raw, err := NormalizeOptions(db, TypeWG, 0, json.RawMessage(`{"junk":1}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := Decode(raw)
	if cfg.ListenPort != DefaultPort || cfg.Subnet != "10.8.1.0/24" || !ValidKey(cfg.PrivateKey) || cfg.PublicKey == "" {
		t.Errorf("cfg = %+v", cfg)
	}
	if !cfg.RouteThroughCore || cfg.Keepalive != 25 || cfg.DNS != "1.1.1.1, 8.8.8.8" || cfg.Version != "" {
		t.Errorf("defaults = %+v", cfg)
	}
	if pub, _ := PublicKey(cfg.PrivateKey); pub != cfg.PublicKey {
		t.Error("public key does not match the private key")
	}
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if _, ok := m["junk"]; ok {
		t.Error("unknown keys must be dropped")
	}
	if m["listen_port"] == nil {
		t.Error("the list reads listen_port")
	}
}

func TestSecondInboundGetsItsOwnPortAndSubnet(t *testing.T) {
	initDB(t)
	db := DB()
	id1 := addInbound(t, db, TypeWG, "wg1", `{}`, true)
	id2 := addInbound(t, db, TypeAWG, "awg1", `{}`, true)
	a, _ := LoadInbound(db, id1)
	b, _ := LoadInbound(db, id2)
	if a.Config.ListenPort == b.Config.ListenPort || a.Config.Subnet == b.Config.Subnet {
		t.Errorf("clash: %+v %+v", a.Config.ListenPort, b.Config.Subnet)
	}
	if b.Config.Version != DefaultAWGVersion || b.Config.Jc == 0 || b.Config.H1 == "" {
		t.Errorf("awg defaults: %+v", b.Config)
	}
	// Explicit collisions are refused, on port and on subnet overlap.
	if _, err := NormalizeOptions(db, TypeWG, 0, json.RawMessage(`{"listen_port":`+itoa(a.Config.ListenPort)+`}`), nil); err == nil {
		t.Error("duplicate port accepted")
	}
	if _, err := NormalizeOptions(db, TypeWG, 0, json.RawMessage(`{"subnet":"10.8.1.128/25"}`), nil); err == nil {
		t.Error("overlapping subnet accepted")
	}
	// ...but an inbound may keep its own port and subnet on edit.
	if _, err := NormalizeOptions(db, TypeWG, id1, json.RawMessage(`{"tag":"x"}`), nil2raw(a)); err != nil {
		t.Errorf("edit refused: %v", err)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func nil2raw(in *Inbound) json.RawMessage { raw, _ := in.Config.Encode(); return raw }

func TestEditKeepsTheKeyAndMergesOverStored(t *testing.T) {
	initDB(t)
	db := DB()
	id := addInbound(t, db, TypeAWG, "awg", `{"version":"3.1"}`, true)
	in, _ := LoadInbound(db, id)
	stored := nil2raw(in)
	raw, err := NormalizeOptions(db, TypeAWG, id, json.RawMessage(`{"dns":"9.9.9.9","mtu":1380}`), stored)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := Decode(raw)
	if cfg.PrivateKey != in.Config.PrivateKey || cfg.HeaderProtectionKey != in.Config.HeaderProtectionKey || cfg.H1 != in.Config.H1 {
		t.Error("an edit that leaves the keys out must keep them")
	}
	if cfg.DNS != "9.9.9.9" || cfg.MTU != 1380 {
		t.Errorf("edit not applied: %+v", cfg)
	}
}

func TestVersionSwitchPrunesAndCompletes(t *testing.T) {
	initDB(t)
	db := DB()
	id := addInbound(t, db, TypeAWG, "awg", `{"version":"3.1"}`, true)
	in, _ := LoadInbound(db, id)
	raw, err := NormalizeOptions(db, TypeAWG, id, json.RawMessage(`{"version":"1.5"}`), nil2raw(in))
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := Decode(raw)
	if cfg.Version != "1.5" || cfg.S3 != 0 || cfg.HeaderProtectionKey != "" || cfg.RandomTrailers || cfg.I1 == "" {
		t.Errorf("1.5 = %+v", cfg)
	}
	raw, err = NormalizeOptions(db, TypeAWG, id, json.RawMessage(`{"version":"3.0"}`), raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ = Decode(raw)
	if cfg.HeaderProtectionKey == "" || cfg.S3 < 12 || cfg.RekeyAfterTime == "" {
		t.Errorf("3.0 = %+v", cfg)
	}
	// A plain WireGuard inbound sheds every obfuscation field.
	raw, err = NormalizeOptions(db, TypeWG, 0, json.RawMessage(`{"version":"3.1","jc":5,"h1":"7"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ = Decode(raw)
	if cfg.Version != "" || cfg.Jc != 0 || cfg.H1 != "" {
		t.Errorf("wg = %+v", cfg)
	}
}

func TestNormalizeRefusesBadInput(t *testing.T) {
	initDB(t)
	db := DB()
	bad := []string{
		`{"listen_port":70000}`, `{"subnet":"nonsense"}`, `{"subnet":"10.8.0.0/8"}`, `{"subnet":"10.8.0.0/31"}`,
		`{"subnet":"fd00::/64"}`, `{"privateKey":"abc"}`, `{"mtu":100}`, `{"keepalive":-1}`, `{"dns":"example.com"}`,
		`{"allowedIps":"everything"}`, `{"endpoint":"a b"}`, `{"endpoint":"a/b"}`,
		`{"outboundTag":"x\ny"}`,
		`{"outboundTag":"does-not-exist"}`,
	}
	for _, o := range bad {
		if _, err := NormalizeOptions(db, TypeWG, 0, json.RawMessage(o), nil); err == nil {
			t.Errorf("accepted %s", o)
		}
	}
	if _, err := NormalizeOptions(db, TypeAWG, 0, json.RawMessage(`{"version":"9.9"}`), nil); err == nil {
		t.Error("unknown version accepted")
	}
	if _, err := NormalizeOptions(db, TypeAWG, 0, json.RawMessage(`{"version":"2.0","s1":20,"s2":76}`), nil); err == nil {
		t.Error("S1+56=S2 accepted")
	}
	if _, err := NormalizeOptions(db, "vless", 0, nil, nil); err == nil {
		t.Error("foreign type accepted")
	}
}

func TestPeersAreAllocatedOncePerBoundClient(t *testing.T) {
	initDB(t)
	db := DB()
	id := addInbound(t, db, TypeWG, "wg", `{"subnet":"10.9.0.0/29"}`, true)
	alice := addClient(t, db, "alice", true, id)
	bob := addClient(t, db, "bob", true, id)
	off := addClient(t, db, "off", false, id)
	other := addClient(t, db, "other", true)
	in, _ := LoadInbound(db, id)

	peers, err := SyncPeers(db, *in)
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 3 {
		t.Fatalf("peers = %+v", peers)
	}
	addrs := map[string]bool{}
	for _, p := range peers {
		if addrs[p.Address] || p.Address == "10.9.0.1" || p.Address == "10.9.0.7" {
			t.Errorf("bad address %s", p.Address)
		}
		addrs[p.Address] = true
		if !ValidKey(p.PrivateKey) || !ValidKey(p.PresharedKey) {
			t.Error("peer keys not generated")
		}
		if pub, _ := PublicKey(p.PrivateKey); pub != p.PublicKey {
			t.Error("peer public key mismatch")
		}
	}
	act := Active(peers)
	if len(act) != 2 {
		t.Errorf("a disabled client must not be accepted: %+v", act)
	}
	// Stable: syncing again gives the same keys and addresses.
	again, _ := SyncPeers(db, *in)
	for i := range peers {
		if peers[i] != again[i] {
			t.Errorf("peer changed between syncs: %+v / %+v", peers[i], again[i])
		}
	}
	if _, ok, _ := PeerFor(db, *in, other); ok {
		t.Error("an unbound client has no peer")
	}
	p, ok, err := PeerFor(db, *in, alice)
	if err != nil || !ok || p.ClientName != "alice" {
		t.Errorf("PeerFor = %+v %v %v", p, ok, err)
	}

	// Unbinding keeps the row (rebinding gives the same config) but stops the peer.
	before := p
	db.Model(&model.Client{}).Where("id = ?", alice).Update("inbounds", json.RawMessage(`[]`))
	peers, _ = SyncPeers(db, *in)
	if len(Active(peers)) != 1 || Active(peers)[0].ClientID != bob {
		t.Errorf("active after unbind = %+v", Active(peers))
	}
	db.Model(&model.Client{}).Where("id = ?", alice).Update("inbounds", json.RawMessage(`[`+itoa(int(id))+`]`))
	back, ok, _ := PeerFor(db, *in, alice)
	if !ok || back != before {
		t.Errorf("rebinding changed the peer: %+v vs %+v", back, before)
	}

	// Deleting a client removes its row and frees the address.
	db.Delete(&model.Client{}, off)
	peers, _ = SyncPeers(db, *in)
	if len(peers) != 2 {
		t.Errorf("peers after delete = %d", len(peers))
	}
	var n int64
	db.Model(&model.WgPeer{}).Where("inbound_id = ?", id).Count(&n)
	if n != 2 {
		t.Errorf("rows = %d", n)
	}
	if err := DeletePeers(db, id); err != nil {
		t.Fatal(err)
	}
	db.Model(&model.WgPeer{}).Count(&n)
	if n != 0 {
		t.Error("DeletePeers left rows")
	}
}

func TestPoolRunsOutCleanlyAndSubnetChangeReaddresses(t *testing.T) {
	initDB(t)
	db := DB()
	id := addInbound(t, db, TypeWG, "wg", `{"subnet":"10.9.0.0/29"}`, true) // .1 server, .2-.6 for clients
	for _, n := range []string{"a", "b", "c", "d", "e", "f"} {
		addClient(t, db, n, true, id)
	}
	in, _ := LoadInbound(db, id)
	peers, err := SyncPeers(db, *in)
	var full *PoolFullError
	if !errors.As(err, &full) || len(peers) != 5 {
		t.Errorf("a full pool must be reported, with the peers that fit kept: %d peers, %v", len(peers), err)
	}
	addrs := map[string]bool{}
	for _, p := range peers {
		addrs[p.Address] = true
	}
	if len(addrs) != 5 {
		t.Error("duplicate addresses")
	}

	id2 := addInbound(t, db, TypeWG, "wg2", `{"subnet":"10.50.0.0/24"}`, true)
	c := addClient(t, db, "c", true, id2)
	in2, _ := LoadInbound(db, id2)
	p1, _, _ := PeerFor(db, *in2, c)
	in2.Config.Subnet = "10.51.0.0/24"
	p2, _, err := PeerFor(db, *in2, c)
	if err != nil {
		t.Fatal(err)
	}
	if p2.Address == p1.Address || p2.Address != "10.51.0.2" || p2.PrivateKey != p1.PrivateKey {
		t.Errorf("peer after subnet change = %+v (was %+v)", p2, p1)
	}
}

func TestInjectCoreAddsOneTunPerRoutedInbound(t *testing.T) {
	if !RoutingSupported() {
		t.Skip("routing is Linux-only")
	}
	initDB(t)
	db := DB()
	if err := db.Exec(`INSERT INTO outbounds(type, tag, options) VALUES ('socks','proxy','{}')`).Error; err != nil {
		t.Fatal(err)
	}
	a := addInbound(t, db, TypeWG, "wg", `{"outboundTag":"proxy"}`, true)
	b := addInbound(t, db, TypeAWG, "awg", `{"routeThroughCore":false}`, true)
	c := addInbound(t, db, TypeWG, "wg3", `{}`, false)
	_, _, _ = b, c, a

	in, route, err := InjectCore([]json.RawMessage{json.RawMessage(`{"type":"vless","tag":"v"}`)},
		json.RawMessage(`{"rules":[{"outbound":"direct"}],"final":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(in) != 2 {
		t.Fatalf("inbounds = %s", in)
	}
	var tun map[string]any
	_ = json.Unmarshal(in[1], &tun)
	if tun["type"] != "tun" || tun["tag"] != TunTag(a) || tun["interface_name"] != TunName(a) || tun["auto_route"] != false {
		t.Errorf("tun = %v", tun)
	}
	var r struct {
		Rules []map[string]any `json:"rules"`
		Final string           `json:"final"`
	}
	_ = json.Unmarshal(route, &r)
	if len(r.Rules) != 3 || r.Rules[0]["action"] != "sniff" || r.Rules[1]["outbound"] != "proxy" || r.Rules[2]["outbound"] != "direct" || r.Final != "x" {
		t.Errorf("rules = %v", r)
	}
	if CoreNeedsRestart() {
		t.Error("the core was just built with this fragment")
	}
	db.Model(&model.Inbound{}).Where("id = ?", c).Update("enable", true)
	if !CoreNeedsRestart() {
		t.Error("switching a routed inbound on changes the core config")
	}
	// Switching off an inbound that does not route through the core does not.
	db.Model(&model.Inbound{}).Where("id = ?", b).Update("enable", false)
	_, _, _ = InjectCore(nil, nil)
	db.Model(&model.Inbound{}).Where("id = ?", b).Update("enable", true)
	if CoreNeedsRestart() {
		t.Error("a non-routed inbound never touches the core")
	}
	// A clashing operator inbound leaves the config alone.
	taken := json.RawMessage(`{"type":"tun","tag":"` + TunTag(a) + `"}`)
	got, _, err := InjectCore([]json.RawMessage{taken}, nil)
	if err == nil || len(got) != 1 {
		t.Error("a tag clash must refuse to inject")
	}
}
