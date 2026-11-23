package qwdtt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
)

// A passwords.json the way the qWDTT server writes it: one entry the operator
// made through the server's bot, one the panel wrote earlier with a device
// bound to it.
const serverFile = `{
  "passwords": {
    "botMade": {"label": "Доступ 1", "device_id": "", "device_ids": ["devB"], "max_devices": 1, "expires_at": 0, "down_bytes": 5, "up_bytes": 6},
    "pw-old": {"label": "3s-ui:carol", "device_id": "", "device_ids": ["devC"], "max_devices": 3, "expires_at": 0, "down_bytes": 100, "up_bytes": 40},
    "pw-alice": {"label": "3s-ui:alice", "device_id": "", "device_ids": ["devA"], "max_devices": 3, "expires_at": 0, "down_bytes": 300, "up_bytes": 70}
  },
  "devices": {
    "devA": {"device_id": "devA", "ip": "10.66.66.2", "priv_key": "a", "pub_key": "A", "down_bytes": 1, "up_bytes": 2, "raw_ip": "10.77.0.2"},
    "devB": {"device_id": "devB", "ip": "10.66.66.3", "priv_key": "b", "pub_key": "B", "down_bytes": 1, "up_bytes": 2},
    "devC": {"device_id": "devC", "ip": "10.66.66.4", "priv_key": "c", "pub_key": "C", "down_bytes": 1, "up_bytes": 2}
  }
}`

func readBack(t *testing.T, dir string) map[string]map[string]map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, passwordsFile))
	if err != nil {
		t.Fatal(err)
	}
	var f map[string]map[string]map[string]any
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestSyncUsersOwnsOnlyItsEntries(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, passwordsFile), []byte(serverFile), 0o600); err != nil {
		t.Fatal(err)
	}
	base := map[string]model.QwdttUsage{
		"pw-alice": {Password: "pw-alice", ClientName: "alice", Up: 50, Down: 200},
		"pw-old":   {Password: "pw-old", ClientName: "carol", Up: 40, Down: 90},
	}
	users := []UserSpec{
		{Client: "alice", Password: "pw-alice", MaxDevices: 5},
		{Client: "dave", Password: "pw-dave", MaxDevices: 3},
	}
	res, err := SyncUsers(dir, users, base)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed {
		t.Fatal("file not rewritten")
	}
	f := readBack(t, dir)
	pw, dev := f["passwords"], f["devices"]
	if _, ok := pw["botMade"]; !ok {
		t.Error("an entry the panel did not write was removed")
	}
	if _, ok := dev["devB"]; !ok {
		t.Error("the bot entry's device was removed")
	}
	if _, ok := pw["pw-old"]; ok {
		t.Error("a client no longer wanted kept its password")
	}
	if _, ok := dev["devC"]; ok {
		t.Error("the removed password's device stayed (its WG peer would too)")
	}
	if a := pw["pw-alice"]; a["max_devices"].(float64) != 5 || a["label"] != "3s-ui:alice" {
		t.Errorf("alice = %v", a)
	}
	if dev["devA"]["raw_ip"] != "10.77.0.2" {
		t.Error("an unknown device field was lost")
	}
	if d := pw["pw-dave"]; d == nil || d["label"] != "3s-ui:dave" || d["expires_at"].(float64) != 0 {
		t.Errorf("dave = %v", d)
	}

	usage := map[string]Usage{}
	for _, u := range res.Usage {
		usage[u.Client] = u
	}
	if u := usage["alice"]; u.Up != 20 || u.Down != 100 {
		t.Errorf("alice usage = %+v, want growth since the baseline", u)
	}
	if u := usage["carol"]; u.Up != 0 || u.Down != 10 {
		t.Errorf("carol usage = %+v, read one last time before removal", u)
	}
	if _, ok := base["pw-old"]; ok {
		t.Error("baseline of a removed password kept")
	}
	if b := base["pw-alice"]; b.Up != 70 || b.Down != 300 {
		t.Errorf("baseline not advanced: %+v", b)
	}

	// The same state again writes nothing and reports no growth.
	res, err = SyncUsers(dir, users, base)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed || len(res.Usage) != 0 {
		t.Errorf("second pass = %+v", res)
	}
}

func TestSyncUsersCounterResetCountsWhole(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, passwordsFile), []byte(`{"passwords":{"p":{"label":"3s-ui:x","up_bytes":7,"down_bytes":9}},"devices":{}}`), 0o600)
	base := map[string]model.QwdttUsage{"p": {Password: "p", Up: 100, Down: 100}}
	res, err := SyncUsers(dir, []UserSpec{{Client: "x", Password: "p", MaxDevices: 1}}, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Usage) != 1 || res.Usage[0].Up != 7 || res.Usage[0].Down != 9 {
		t.Errorf("usage = %+v", res.Usage)
	}
}

func TestSyncUsersNoFileYet(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	res, err := SyncUsers(dir, []UserSpec{{Client: "a", Password: "pa", MaxDevices: 2}}, map[string]model.QwdttUsage{})
	if err != nil || !res.Changed {
		t.Fatalf("%+v %v", res, err)
	}
	st, err := os.Stat(filepath.Join(dir, passwordsFile))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("file mode %v, %v", st, err)
	}
}

func TestDesiredUsersAndPasswords(t *testing.T) {
	initDB(t)
	db := database.GetDB()
	in := &model.Inbound{Type: InboundType, Tag: "qwdtt", Enable: true, Addrs: json.RawMessage("null"), OutJson: json.RawMessage("null"), Options: json.RawMessage(`{}`)}
	if err := db.Create(in).Error; err != nil {
		t.Fatal(err)
	}
	ids, _ := json.Marshal([]uint{in.Id})
	now := time.Now().Unix()
	clients := []model.Client{
		{Name: "ok", Enable: true, Inbounds: ids, Config: json.RawMessage(`{}`)},
		{Name: "off", Enable: false, Inbounds: ids, Config: json.RawMessage(`{"qwdtt":{"password":"p-off"}}`)},
		{Name: "expired", Enable: true, Expiry: now - 10, Inbounds: ids, Config: json.RawMessage(`{"qwdtt":{"password":"p-exp"}}`)},
		{Name: "quota", Enable: true, Volume: 100, Up: 60, Down: 50, Inbounds: ids, Config: json.RawMessage(`{"qwdtt":{"password":"p-q"}}`)},
		{Name: "limited", Enable: true, LimitIp: 7, Inbounds: ids, Config: json.RawMessage(`{"qwdtt":{"password":"p-lim"}}`)},
		{Name: "synced", Enable: true, Group: "@cluster", Inbounds: ids, Config: json.RawMessage(`{}`)},
		{Name: "unbound", Enable: true, Inbounds: json.RawMessage(`[]`), Config: json.RawMessage(`{}`)},
	}
	for i := range clients {
		if err := db.Create(&clients[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	n, err := EnsureClientPasswords(db)
	if err != nil || n != 1 {
		t.Fatalf("filled %d, %v (want only the one local client without a password)", n, err)
	}
	var ok model.Client
	db.Where("name = ?", "ok").First(&ok)
	if p := ClientPassword(ok.Config); len(p) != 16 {
		t.Errorf("generated password %q", p)
	}
	cfg := Default()
	users, err := DesiredUsers(db, in.Id, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, u := range users {
		names = append(names, u.Client)
		if u.Client == "limited" && u.MaxDevices != 7 {
			t.Errorf("IP limit not used as the device cap: %+v", u)
		}
		if u.Client == "ok" && u.MaxDevices != DefaultMaxDevices {
			t.Errorf("default cap: %+v", u)
		}
	}
	if strings.Join(names, ",") != "ok,limited" {
		t.Errorf("users = %v", names)
	}
}

func TestSyncUsersCreditsTraffic(t *testing.T) {
	initDB(t)
	db := database.GetDB()
	dir := t.TempDir()
	cfg := Default()
	cfg.ConfigDir = dir
	in := &model.Inbound{Type: InboundType, Tag: "qwdtt", Enable: true, Addrs: json.RawMessage("null"), OutJson: json.RawMessage("null"), Options: json.RawMessage(`{}`)}
	db.Create(in)
	ids, _ := json.Marshal([]uint{in.Id})
	db.Create(&model.Client{Name: "alice", Enable: true, Inbounds: ids, Config: json.RawMessage(`{"qwdtt":{"password":"pw-alice"}}`)})
	changed, err := syncUsers(db, in.Id, cfg)
	if err != nil || !changed {
		t.Fatalf("%v %v", changed, err)
	}
	// The server counts traffic into the entry and saves.
	f := readBack(t, dir)
	f["passwords"]["pw-alice"]["up_bytes"] = 1000
	f["passwords"]["pw-alice"]["down_bytes"] = 5000
	raw, _ := json.Marshal(f)
	_ = os.WriteFile(filepath.Join(dir, passwordsFile), raw, 0o600)
	if _, err := syncUsers(db, in.Id, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := syncUsers(db, in.Id, cfg); err != nil {
		t.Fatal(err)
	}
	var c model.Client
	db.Where("name = ?", "alice").First(&c)
	if c.Up != 1000 || c.Down != 5000 {
		t.Errorf("client traffic = %d/%d, want 1000/5000 counted once", c.Up, c.Down)
	}
	var got model.Inbound
	db.Where("id = ?", in.Id).First(&got)
	if got.Up != 1000 || got.Down != 5000 {
		t.Errorf("inbound traffic = %d/%d", got.Up, got.Down)
	}
	// Switched off: the entry goes, the baseline with it.
	db.Model(model.Client{}).Where("name = ?", "alice").Update("enable", false)
	if changed, err := syncUsers(db, in.Id, cfg); err != nil || !changed {
		t.Fatalf("%v %v", changed, err)
	}
	if _, ok := readBack(t, dir)["passwords"]["pw-alice"]; ok {
		t.Error("disabled client kept its password")
	}
	var n int64
	db.Model(model.QwdttUsage{}).Count(&n)
	if n != 0 {
		t.Errorf("%d baselines left", n)
	}
}

func TestDiagKey(t *testing.T) {
	dir := t.TempDir()
	if DiagKey(dir) != "" {
		t.Error("key without a file")
	}
	_ = os.WriteFile(filepath.Join(dir, "diag.key"), []byte(strings.Repeat("ab", 32)+"\n"), 0o600)
	if k := DiagKey(dir); len(k) != 43 || strings.ContainsAny(k, "=+/") {
		t.Errorf("dk = %q", k)
	}
}

func TestLinkRoundTrip(t *testing.T) {
	c := Default()
	c.SubHost, c.Remark, c.VkHashes = "h.example:56000", "Home", "a,b"
	c.ListenDirect = "0.0.0.0:56004"
	link := c.ClientURIFor("pw", "dk1")
	if !strings.HasPrefix(link, "qwdtt://config?name=Home&peer=h.example%3A56000&hashes=a%2Cb&workers=16&port=9000&pass=pw&dk=dk1&raw=56003&direct=56004") {
		t.Errorf("link = %s", link)
	}
	p, ok := ParseURI(link)
	if !ok || p.Password != "pw" || p.Peer != "h.example:56000" || p.RawPort != 56003 || p.DirectPort != 56004 || p.DiagKey != "dk1" {
		t.Errorf("parsed = %+v %v", p, ok)
	}
	if c.ClientURIFor("", "") != "" {
		t.Error("link without a password")
	}
}
