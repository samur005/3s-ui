package qwdtt

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/samur005/3s-ui/database/jsonsql"
	"github.com/samur005/3s-ui/database/model"
	"github.com/samur005/3s-ui/logger"
	"gorm.io/gorm"
)

// Per-client passwords.
//
// The qWDTT server is multi-user: besides the owner password it takes on the
// command line it keeps generated passwords in <config dir>/passwords.json,
// each with a device cap, an expiry, a deactivated flag and byte counters, and
// re-reads the file on SIGHUP (server/main.go, database_bot.go reloadDB). The
// panel drives that file in per-client mode:
//
//   - every enabled panel client bound to the qWDTT inbound owns one entry,
//     its password being config.qwdtt.password of the client (so a master's
//     @cluster push carries it to a node unchanged, and the master can build
//     the node's link itself);
//   - a disabled, expired or over-quota client has no entry at all: removing
//     it (and the devices bound to it) is what makes the server drop the WG
//     peers of a running session, which deactivating does not;
//   - entries the panel did not write (no "3s-ui:" label) are left alone;
//   - the counters are read back on every pass and the growth credited to the
//     client and the inbound (QwdttUsage keeps the last reading).
//
// The server's own admin API is not used on purpose: it only creates
// passwords it generates itself, caps them at ten and needs TLS and a token.

// ClientKey is the client config block the per-client password lives in.
const ClientKey = "qwdtt"

// labelPrefix marks the passwords.json entries the panel owns.
const labelPrefix = "3s-ui:"

const passwordsFile = "passwords.json"

// ClientPassword returns the client's qWDTT password, "" when it has none.
func ClientPassword(config json.RawMessage) string {
	var cfg map[string]json.RawMessage
	if len(config) == 0 || json.Unmarshal(config, &cfg) != nil {
		return ""
	}
	var blk struct {
		Password string `json:"password"`
	}
	if raw, ok := cfg[ClientKey]; ok {
		_ = json.Unmarshal(raw, &blk)
	}
	return strings.TrimSpace(blk.Password)
}

// NewClientBlock is the config block a new client gets.
func NewClientBlock(name string) (map[string]any, error) {
	p, err := generatePassword(16)
	if err != nil {
		return nil, err
	}
	return map[string]any{"name": name, "password": p}, nil
}

// withClientPassword returns config with a qwdtt block added.
func withClientPassword(config json.RawMessage, name string) (json.RawMessage, string, error) {
	cfg := map[string]json.RawMessage{}
	if len(config) > 0 && string(config) != "null" {
		if err := json.Unmarshal(config, &cfg); err != nil {
			return nil, "", err
		}
	}
	blk, err := NewClientBlock(name)
	if err != nil {
		return nil, "", err
	}
	raw, _ := json.Marshal(blk)
	cfg[ClientKey] = raw
	out, err := json.Marshal(cfg)
	return out, blk["password"].(string), err
}

// clusterGroup is the client group a master's synced clients have on a node
// (service.clusterGroup). Their config is the master's to write.
const clusterGroup = "@cluster"

// EnsureClientPasswords gives every client bound to a qWDTT inbound -- this
// panel's own or a node's replica -- a password of its own when it has none.
// Clients a master synced onto this panel are skipped: their config is the
// master's, and a password made up here would be pushed back over on the next
// sync. It returns how many clients it changed.
func EnsureClientPasswords(db *gorm.DB) (int, error) {
	var ids []uint
	if err := db.Model(model.Inbound{}).Where("type = ?", InboundType).Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	var clients []model.Client
	if err := db.Model(model.Client{}).
		Where("id IN (SELECT DISTINCT clients.id FROM clients, "+jsonsql.Each("clients.inbounds")+" AS je WHERE je.value IN ?)", ids).
		Find(&clients).Error; err != nil {
		return 0, err
	}
	n := 0
	for _, c := range clients {
		if c.Group == clusterGroup || ClientPassword(c.Config) != "" {
			continue
		}
		cfg, _, err := withClientPassword(c.Config, c.Name)
		if err != nil {
			logger.Warning("qwdtt: client ", c.Name, ": config is not JSON: ", err)
			continue
		}
		if err := db.Model(model.Client{}).Where("id = ?", c.Id).Update("config", cfg).Error; err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// UserSpec is one password the server should hold.
type UserSpec struct {
	Client     string
	Password   string
	MaxDevices int
}

// clientActive is the panel's own verdict on a client: switched on, not
// expired, under its quota.
func clientActive(c *model.Client, now int64) bool {
	if !c.Enable {
		return false
	}
	if c.Expiry > 0 && c.Expiry <= now {
		return false
	}
	if c.Volume > 0 && c.Up+c.Down >= c.Volume {
		return false
	}
	return true
}

// boundClients lists the clients bound to an inbound.
func boundClients(db *gorm.DB, inboundID uint) ([]model.Client, error) {
	var clients []model.Client
	err := db.Model(model.Client{}).
		Where("id IN (SELECT DISTINCT clients.id FROM clients, "+jsonsql.Each("clients.inbounds")+" AS je WHERE je.value = ?)", inboundID).
		Order("id").Find(&clients).Error
	return clients, err
}

// DesiredUsers lists the passwords the server should hold for the inbound.
// Two clients that somehow share a password (a copied config) keep only the
// first: the server keys entries by password.
func DesiredUsers(db *gorm.DB, inboundID uint, cfg Config) ([]UserSpec, error) {
	clients, err := boundClients(db, inboundID)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	seen := map[string]bool{strings.TrimSpace(cfg.Password): true}
	var out []UserSpec
	for i := range clients {
		c := &clients[i]
		if !clientActive(c, now) {
			continue
		}
		pass := ClientPassword(c.Config)
		if pass == "" || seen[pass] {
			continue
		}
		seen[pass] = true
		max := cfg.MaxDevices
		if max <= 0 {
			max = DefaultMaxDevices
		}
		if c.LimitIp > 0 {
			max = c.LimitIp
		}
		out = append(out, UserSpec{Client: c.Name, Password: pass, MaxDevices: max})
	}
	return out, nil
}

// pwFile is passwords.json, kept as loose maps so fields this panel does not
// know (device keys, raw-IP state, a newer server's additions) survive the
// round trip untouched.
type pwFile struct {
	Passwords map[string]map[string]any  `json:"passwords"`
	Devices   map[string]map[string]any  `json:"devices"`
	Extra     map[string]json.RawMessage `json:"-"`
}

func readPwFile(path string) (*pwFile, error) {
	f := &pwFile{Passwords: map[string]map[string]any{}, Devices: map[string]map[string]any{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return nil, err
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, err
	}
	if raw, ok := top["passwords"]; ok {
		_ = json.Unmarshal(raw, &f.Passwords)
	}
	if raw, ok := top["devices"]; ok {
		_ = json.Unmarshal(raw, &f.Devices)
	}
	delete(top, "passwords")
	delete(top, "devices")
	f.Extra = top
	if f.Passwords == nil {
		f.Passwords = map[string]map[string]any{}
	}
	if f.Devices == nil {
		f.Devices = map[string]map[string]any{}
	}
	return f, nil
}

func (f *pwFile) encode() ([]byte, error) {
	top := map[string]any{}
	for k, v := range f.Extra {
		top[k] = v
	}
	top["passwords"] = f.Passwords
	top["devices"] = f.Devices
	return json.MarshalIndent(top, "", "  ")
}

// writeFileAtomic writes the file the way the server does: a 0600 temp file
// in the same directory renamed over the old one, so the server never reads a
// half-written file.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".passwords-panel-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(0o600); err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

func num(v any) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case int64:
		return t
	case int:
		return int64(t)
	case json.Number:
		n, _ := t.Int64()
		return n
	}
	return 0
}

func managedLabel(e map[string]any) (string, bool) {
	l, _ := e["label"].(string)
	name, ok := strings.CutPrefix(l, labelPrefix)
	return name, ok
}

func entryDevices(e map[string]any) []string {
	var out []string
	if ids, ok := e["device_ids"].([]any); ok {
		for _, v := range ids {
			if s, ok := v.(string); ok && s != "" {
				out = append(out, s)
			}
		}
	}
	if s, ok := e["device_id"].(string); ok && s != "" && s != "multi" {
		out = append(out, s)
	}
	return out
}

// Usage is the growth of one client's counters since the previous reading.
type Usage struct {
	Client string
	Up     int64
	Down   int64
}

// SyncResult is what one pass over passwords.json did.
type SyncResult struct {
	// Changed is whether the file was rewritten; the running server has to be
	// told (SIGHUP) to re-read it.
	Changed bool
	Users   int
	Usage   []Usage
}

// SyncUsers makes passwords.json in dir hold exactly the panel's users (and
// whatever else the panel did not write) and reports the counters' growth.
// baselines holds the previous reading per password and is updated in place;
// the caller persists it.
func SyncUsers(dir string, users []UserSpec, baselines map[string]model.QwdttUsage) (SyncResult, error) {
	var res SyncResult
	path := filepath.Join(dir, passwordsFile)
	f, err := readPwFile(path)
	if err != nil {
		return res, err
	}
	before, err := f.encode()
	if err != nil {
		return res, err
	}

	// Traffic first, from what the server last saved -- an entry about to be
	// removed is read one last time.
	for pass, e := range f.Passwords {
		name, ok := managedLabel(e)
		if !ok || e == nil {
			continue
		}
		up, down := num(e["up_bytes"]), num(e["down_bytes"])
		base, had := baselines[pass]
		du, dd := up-base.Up, down-base.Down
		if !had {
			du, dd = up, down
		}
		if du < 0 {
			du = up
		}
		if dd < 0 {
			dd = down
		}
		if du > 0 || dd > 0 {
			res.Usage = append(res.Usage, Usage{Client: name, Up: du, Down: dd})
		}
		baselines[pass] = model.QwdttUsage{Password: pass, ClientName: name, Up: up, Down: down}
	}

	want := make(map[string]UserSpec, len(users))
	for _, u := range users {
		want[u.Password] = u
	}
	// Drop the panel's entries no client wants any more, with their devices.
	for pass, e := range f.Passwords {
		if _, ok := managedLabel(e); !ok {
			continue
		}
		if _, keep := want[pass]; keep {
			continue
		}
		for _, d := range entryDevices(e) {
			delete(f.Devices, d)
		}
		delete(f.Passwords, pass)
	}
	for pass := range baselines {
		if _, ok := f.Passwords[pass]; !ok {
			if _, wanted := want[pass]; !wanted {
				delete(baselines, pass)
			}
		}
	}
	// Add or update the wanted ones. expires_at stays 0: the panel decides
	// expiry itself (by removing the entry), so the server never deletes an
	// entry the next pass would only put back.
	for _, u := range users {
		e := f.Passwords[u.Password]
		if e == nil {
			e = map[string]any{"device_id": "", "device_ids": []any{}, "down_bytes": 0, "up_bytes": 0}
			f.Passwords[u.Password] = e
			baselines[u.Password] = model.QwdttUsage{Password: u.Password, ClientName: u.Client}
		}
		e["label"] = labelPrefix + u.Client
		e["max_devices"] = u.MaxDevices
		e["expires_at"] = 0
		delete(e, "is_deactivated")
	}
	res.Users = len(users)

	after, err := f.encode()
	if err != nil {
		return res, err
	}
	if jsonSame(before, after) {
		return res, nil
	}
	if err := writeFileAtomic(path, after); err != nil {
		return res, err
	}
	res.Changed = true
	return res, nil
}

func jsonSame(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	ax, _ := json.Marshal(x)
	by, _ := json.Marshal(y)
	return string(ax) == string(by)
}

// ServerUser is one entry of passwords.json as the qWDTT page lists it.
type ServerUser struct {
	Client        string `json:"client"`
	Managed       bool   `json:"managed"`
	MaxDevices    int64  `json:"maxDevices"`
	Devices       int    `json:"devices"`
	Up            int64  `json:"up"`
	Down          int64  `json:"down"`
	IsDeactivated bool   `json:"isDeactivated"`
}

// ListServerUsers reads passwords.json for the page. Passwords themselves are
// not returned.
func ListServerUsers(dir string) ([]ServerUser, error) {
	f, err := readPwFile(filepath.Join(dir, passwordsFile))
	if err != nil {
		return nil, err
	}
	out := make([]ServerUser, 0, len(f.Passwords))
	for _, e := range f.Passwords {
		if e == nil {
			continue
		}
		name, managed := managedLabel(e)
		if !managed {
			name, _ = e["label"].(string)
		}
		deact, _ := e["is_deactivated"].(bool)
		out = append(out, ServerUser{
			Client: name, Managed: managed, MaxDevices: num(e["max_devices"]),
			Devices: len(entryDevices(e)), Up: num(e["up_bytes"]), Down: num(e["down_bytes"]),
			IsDeactivated: deact,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Client < out[j].Client })
	return out, nil
}

// DiagKey is the dk= value of the server's links: its diag.key (32 random
// bytes written in hex, server/netdiag.go) in unpadded base64url. "" until
// the server has created the file.
func DiagKey(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "diag.key"))
	if err != nil {
		return ""
	}
	secret, err := hex.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(secret) != 32 {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(secret)
}

// loadBaselines reads the previous counter readings.
func loadBaselines(db *gorm.DB) (map[string]model.QwdttUsage, error) {
	var rows []model.QwdttUsage
	if err := db.Model(model.QwdttUsage{}).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]model.QwdttUsage, len(rows))
	for _, r := range rows {
		out[r.Password] = r
	}
	return out, nil
}

func sameBaselines(a, b map[string]model.QwdttUsage) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// syncUsers is one per-client pass: passwords are filled in for clients that
// lack one, passwords.json is brought in line, and the traffic read from it
// is credited to the clients and the inbound. It reports whether the file
// changed.
func syncUsers(db *gorm.DB, inboundID uint, cfg Config) (bool, error) {
	if _, err := EnsureClientPasswords(db); err != nil {
		return false, err
	}
	users, err := DesiredUsers(db, inboundID, cfg)
	if err != nil {
		return false, err
	}
	baselines, err := loadBaselines(db)
	if err != nil {
		return false, err
	}
	prev := make(map[string]model.QwdttUsage, len(baselines))
	for k, v := range baselines {
		prev[k] = v
	}
	res, err := SyncUsers(cfg.ResolveConfigDir(), users, baselines)
	if err != nil {
		return false, err
	}
	if len(res.Usage) == 0 && sameBaselines(prev, baselines) {
		return res.Changed, nil
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		var totalUp, totalDown int64
		now := time.Now().Unix()
		for _, u := range res.Usage {
			if err := tx.Model(model.Client{}).Where("name = ?", u.Client).Updates(map[string]any{
				"up":        gorm.Expr("up + ?", u.Up),
				"down":      gorm.Expr("down + ?", u.Down),
				"online_at": now,
			}).Error; err != nil {
				return err
			}
			totalUp += u.Up
			totalDown += u.Down
		}
		if totalUp > 0 || totalDown > 0 {
			if err := tx.Model(model.Inbound{}).Where("id = ?", inboundID).Updates(map[string]any{
				"up":   gorm.Expr("up + ?", totalUp),
				"down": gorm.Expr("down + ?", totalDown),
			}).Error; err != nil {
				return err
			}
		}
		for k := range prev {
			if _, ok := baselines[k]; !ok {
				if err := tx.Where("password = ?", k).Delete(&model.QwdttUsage{}).Error; err != nil {
					return err
				}
			}
		}
		for k, v := range baselines {
			if old, ok := prev[k]; ok && old == v {
				continue
			}
			if err := tx.Save(&v).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return res.Changed, err
}
