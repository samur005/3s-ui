package wgserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"sync"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/jsonsql"
	"github.com/samur005/3s-ui/database/model"
	"gorm.io/gorm"
)

// Inbound is a stored WireGuard/AmneziaWG inbound with its decoded options.
type Inbound struct {
	ID     uint
	Type   string
	Tag    string
	Enable bool
	Config Config
}

// storeMu serializes peer allocation: two requests binding clients at once
// must not be handed the same address.
var storeMu sync.Mutex

// LoadInbounds returns every local WireGuard/AmneziaWG inbound, ordered by id.
// A row whose options cannot be decoded is skipped (and reported by the
// caller's log through the error list) rather than stopping the others.
func LoadInbounds(db *gorm.DB) ([]Inbound, error) {
	var rows []model.Inbound
	err := db.Model(model.Inbound{}).
		Where("type IN ? AND node_id IS NULL", Types()).Order("id").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]Inbound, 0, len(rows))
	for _, r := range rows {
		cfg, derr := Decode(r.Options)
		if derr != nil {
			continue
		}
		out = append(out, Inbound{ID: r.Id, Type: r.Type, Tag: r.Tag, Enable: r.Enable, Config: cfg})
	}
	return out, nil
}

// LoadInbound returns one inbound by id, nil when it is not a local
// WireGuard/AmneziaWG row.
func LoadInbound(db *gorm.DB, id uint) (*Inbound, error) {
	all, err := LoadInbounds(db)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ID == id {
			return &all[i], nil
		}
	}
	return nil, nil
}

// defaults are what a new inbound of the type starts from.
func defaults(typ string) Config {
	c := Config{
		MTU: 0, DNS: DefaultDNS, Keepalive: DefaultKeepalive, AllowedIPs: DefaultAllowed,
		RouteThroughCore: true,
	}
	if typ == TypeAWG {
		c.Version = DefaultAWGVersion
	}
	return c
}

// otherInbounds are the rows a new or edited inbound must not collide with.
func otherInbounds(tx *gorm.DB, id uint) ([]Inbound, error) {
	all, err := LoadInbounds(tx)
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, in := range all {
		if in.ID != id {
			out = append(out, in)
		}
	}
	return out, nil
}

func overlaps(a, b netip.Prefix) bool { return a.Overlaps(b) }

// NormalizeOptions turns the options a form sent into the stored shape. stored
// holds the row's current options (nil for a new row): what the form leaves out
// keeps its stored value, so an edit that omits the private key does not
// rotate it. Missing parts are generated -- port, subnet, keys, obfuscation --
// and everything is validated, including collisions with the other inbounds of
// this package. id is the row's own id (0 for a new row).
func NormalizeOptions(tx *gorm.DB, typ string, id uint, options, stored json.RawMessage) (json.RawMessage, error) {
	if !IsType(typ) {
		return nil, fmt.Errorf("unknown inbound type %q", typ)
	}
	cfg := defaults(typ)
	if len(stored) > 0 && string(stored) != "null" {
		if err := json.Unmarshal(stored, &cfg); err != nil {
			return nil, fmt.Errorf("stored options are damaged: %w", err)
		}
	}
	if len(options) > 0 && string(options) != "null" {
		if err := json.Unmarshal(options, &cfg); err != nil {
			return nil, fmt.Errorf("invalid options: %w", err)
		}
	}
	others, err := otherInbounds(tx, id)
	if err != nil {
		return nil, err
	}

	// Port.
	if cfg.ListenPort == 0 {
		cfg.ListenPort = pickPort(others)
	}
	if cfg.ListenPort < 1 || cfg.ListenPort > 65535 {
		return nil, fmt.Errorf("port %d is out of range", cfg.ListenPort)
	}
	for _, o := range others {
		if o.Config.ListenPort == cfg.ListenPort {
			return nil, fmt.Errorf("UDP port %d is already used by the inbound %q", cfg.ListenPort, o.Tag)
		}
	}

	// Keys.
	cfg.PrivateKey = strings.TrimSpace(cfg.PrivateKey)
	if cfg.PrivateKey == "" {
		priv, _, err := NewKeyPair()
		if err != nil {
			return nil, err
		}
		cfg.PrivateKey = priv
	}
	pub, err := PublicKey(cfg.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("private key: %w", err)
	}
	cfg.PublicKey = pub

	// Subnet.
	cfg.Subnet = strings.TrimSpace(cfg.Subnet)
	if cfg.Subnet == "" {
		s, err := pickSubnet(others)
		if err != nil {
			return nil, err
		}
		cfg.Subnet = s
	}
	sp, err := netip.ParsePrefix(cfg.Subnet)
	if err != nil || !sp.Addr().Is4() {
		return nil, fmt.Errorf("subnet %q must be an IPv4 network such as 10.8.1.0/24", cfg.Subnet)
	}
	if sp.Bits() < 16 || sp.Bits() > 29 {
		return nil, fmt.Errorf("subnet %q: the prefix length must be between 16 and 29", cfg.Subnet)
	}
	sp = sp.Masked()
	cfg.Subnet = sp.String()
	for _, o := range others {
		if op, err := netip.ParsePrefix(o.Config.Subnet); err == nil && overlaps(sp, op) {
			return nil, fmt.Errorf("subnet %s overlaps %s of the inbound %q", sp, op, o.Tag)
		}
	}

	// Network options.
	if cfg.MTU != 0 && (cfg.MTU < 576 || cfg.MTU > 1500) {
		return nil, fmt.Errorf("MTU %d must be 0 (automatic) or between 576 and 1500", cfg.MTU)
	}
	if cfg.Keepalive < 0 || cfg.Keepalive > 3600 {
		return nil, fmt.Errorf("keepalive %d must be 0..3600 seconds", cfg.Keepalive)
	}
	dns := splitList(cfg.DNS)
	for _, d := range dns {
		if _, err := netip.ParseAddr(d); err != nil {
			return nil, fmt.Errorf("DNS %q is not an IP address", d)
		}
	}
	cfg.DNS = strings.Join(dns, ", ")
	allowed := splitList(cfg.AllowedIPs)
	if len(allowed) == 0 {
		allowed = splitList(DefaultAllowed)
	}
	for _, a := range allowed {
		if _, err := netip.ParsePrefix(a); err != nil {
			return nil, fmt.Errorf("allowed IPs: %q is not a network such as 0.0.0.0/0", a)
		}
	}
	cfg.AllowedIPs = strings.Join(allowed, ", ")
	cfg.Endpoint = strings.TrimSpace(cfg.Endpoint)
	if err := checkEndpoint(cfg.Endpoint); err != nil {
		return nil, err
	}
	cfg.OutboundTag = strings.TrimSpace(cfg.OutboundTag)
	if err := noControl("outbound", cfg.OutboundTag); err != nil {
		return nil, err
	}
	if cfg.OutboundTag != "" {
		if err := outboundTagExists(tx, cfg.OutboundTag); err != nil {
			return nil, err
		}
	}

	// Obfuscation.
	if typ == TypeWG {
		cfg.Version = ""
		cfg.Obfuscation = Obfuscation{}
	} else {
		cfg.Version = strings.TrimSpace(cfg.Version)
		if cfg.Version == "" {
			cfg.Version = DefaultAWGVersion
		}
		if !ValidVersion(cfg.Version) {
			return nil, fmt.Errorf("AmneziaWG version %q is not one of %s", cfg.Version, strings.Join(AWGVersions, ", "))
		}
		o := cfg.Obfuscation
		for _, h := range []*string{&o.H1, &o.H2, &o.H3, &o.H4, &o.ContentPaddingAddition,
			&o.RekeyAfterTime, &o.RekeyTimeout, &o.RejectAfterTime, &o.KeepaliveTimeout, &o.MaxHandshakeAttempts} {
			*h = strings.ReplaceAll(strings.TrimSpace(*h), " ", "")
		}
		for _, c := range []*string{&o.I1, &o.I2, &o.I3, &o.I4, &o.I5, &o.HeaderProtectionKey} {
			*c = strings.TrimSpace(*c)
		}
		o = o.FillMissing(cfg.Version)
		if err := o.Validate(cfg.Version); err != nil {
			return nil, err
		}
		cfg.Obfuscation = o
	}
	return cfg.Encode()
}

func checkEndpoint(e string) error {
	if e == "" {
		return nil
	}
	if err := noControl("endpoint", e); err != nil {
		return err
	}
	if strings.ContainsAny(e, " \t/?#@") || len(e) > 255 {
		return fmt.Errorf("endpoint %q must be a host or host:port", e)
	}
	return nil
}

func pickPort(others []Inbound) int {
	used := map[int]bool{}
	for _, o := range others {
		used[o.Config.ListenPort] = true
	}
	p := DefaultPort
	for used[p] {
		p++
	}
	return p
}

// pickSubnet takes the first 10.8.N.0/24 no other inbound overlaps.
func pickSubnet(others []Inbound) (string, error) {
	for n := 1; n < 250; n++ {
		cand := netip.MustParsePrefix(fmt.Sprintf("10.8.%d.0/24", n))
		free := true
		for _, o := range others {
			if op, err := netip.ParsePrefix(o.Config.Subnet); err == nil && overlaps(cand, op) {
				free = false
				break
			}
		}
		if free {
			return cand.String(), nil
		}
	}
	return "", fmt.Errorf("no free 10.8.N.0/24 subnet left: set one by hand")
}

// Peer is a client's identity on one inbound, joined with the client's state.
type Peer struct {
	ClientID     uint
	ClientName   string
	ClientEnable bool
	Bound        bool
	PrivateKey   string
	PublicKey    string
	PresharedKey string
	Address      string
}

// boundClients returns id, name and enable of the clients bound to an inbound.
func boundClients(db *gorm.DB, inboundID uint) ([]model.Client, error) {
	var rows []model.Client
	err := db.Raw("SELECT clients.id, clients.name, clients.enable FROM clients, "+
		jsonsql.Each("clients.inbounds")+" AS je WHERE je.value = ?", inboundID).Scan(&rows).Error
	return rows, err
}

// SyncPeers makes sure every client bound to the inbound has a peer row (keys
// and an address from the pool), drops the rows of clients that no longer
// exist, re-addresses peers that fell outside a changed subnet, and returns all
// the inbound's peers. Peers of clients that are unbound stay (Bound false) so
// that rebinding returns the same config, but the server must not accept them.
func SyncPeers(db *gorm.DB, in Inbound) ([]Peer, error) {
	storeMu.Lock()
	defer storeMu.Unlock()
	return syncPeersLocked(db, in)
}

func syncPeersLocked(db *gorm.DB, in Inbound) ([]Peer, error) {
	clients, err := boundClients(db, in.ID)
	if err != nil {
		return nil, err
	}
	bound := map[uint]model.Client{}
	for _, c := range clients {
		bound[c.Id] = c
	}
	var rows []model.WgPeer
	if err := db.Where("inbound_id = ?", in.ID).Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	// Rows of deleted clients go.
	var existing []uint
	if err := db.Model(&model.Client{}).Pluck("id", &existing).Error; err != nil {
		return nil, err
	}
	live := map[uint]bool{}
	for _, id := range existing {
		live[id] = true
	}
	keep := rows[:0]
	for _, r := range rows {
		if !live[r.ClientId] {
			if err := db.Delete(&model.WgPeer{}, r.Id).Error; err != nil {
				return nil, err
			}
			continue
		}
		keep = append(keep, r)
	}
	rows = keep

	sub, perr := netip.ParsePrefix(in.Config.Subnet)
	if perr != nil {
		return nil, fmt.Errorf("inbound %q: invalid subnet", in.Tag)
	}
	server := sub.Masked().Addr().Next()
	used := map[netip.Addr]bool{server: true}
	inRange := func(a netip.Addr) bool { return sub.Contains(a) && a != server && a != lastAddr(sub) }
	byClient := map[uint]*model.WgPeer{}
	for i := range rows {
		r := &rows[i]
		if a, err := netip.ParseAddr(r.Address); err == nil && inRange(a) && !used[a] {
			used[a] = true
			byClient[r.ClientId] = r
			continue
		}
		r.Address = "" // to be re-allocated below
		byClient[r.ClientId] = r
	}
	alloc := func() (string, error) {
		for a := server.Next(); sub.Contains(a); a = a.Next() {
			if a == lastAddr(sub) {
				break
			}
			if !used[a] {
				used[a] = true
				return a.String(), nil
			}
		}
		return "", fmt.Errorf("pool full")
	}
	// Existing rows needing a new address first, then rows for new clients.
	for _, r := range rows {
		if r.Address == "" {
			a, err := alloc()
			if err != nil {
				return nil, err
			}
			r.Address = a
			if err := db.Model(&model.WgPeer{}).Where("id = ?", r.Id).Update("address", a).Error; err != nil {
				return nil, err
			}
			byClient[r.ClientId].Address = a
		}
	}
	var full *PoolFullError
	ids := make([]uint, 0, len(bound))
	for id := range bound {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, cid := range ids {
		if byClient[cid] != nil {
			continue
		}
		priv, pub, err := NewKeyPair()
		if err != nil {
			return nil, err
		}
		psk, err := NewPresharedKey()
		if err != nil {
			return nil, err
		}
		a, err := alloc()
		if err != nil {
			// Clients that already have a peer keep working; the ones that do
			// not are reported.
			full = &PoolFullError{Tag: in.Tag, Subnet: in.Config.Subnet}
			break
		}
		row := model.WgPeer{InboundId: in.ID, ClientId: cid, PrivateKey: priv, PublicKey: pub, PresharedKey: psk, Address: a}
		if err := db.Create(&row).Error; err != nil {
			return nil, err
		}
		rows = append(rows, row)
		byClient[cid] = &rows[len(rows)-1]
	}
	// Names of every client with a row.
	names := map[uint]model.Client{}
	if len(byClient) > 0 {
		var ids2 []uint
		for id := range byClient {
			ids2 = append(ids2, id)
		}
		var cs []model.Client
		if err := db.Select("id", "name", "enable").Where("id IN ?", ids2).Find(&cs).Error; err != nil {
			return nil, err
		}
		for _, c := range cs {
			names[c.Id] = c
		}
	}
	out := make([]Peer, 0, len(byClient))
	for _, r := range byClient {
		c := names[r.ClientId]
		_, isBound := bound[r.ClientId]
		out = append(out, Peer{
			ClientID: r.ClientId, ClientName: c.Name, ClientEnable: c.Enable, Bound: isBound,
			PrivateKey: r.PrivateKey, PublicKey: r.PublicKey, PresharedKey: r.PresharedKey, Address: r.Address,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ClientID < out[j].ClientID })
	if full != nil {
		return out, full
	}
	return out, nil
}

// PoolFullError says some bound clients got no peer because the subnet has no
// free address left. The peers that exist are still returned alongside it.
type PoolFullError struct{ Tag, Subnet string }

func (e *PoolFullError) Error() string {
	return fmt.Sprintf("inbound %q: the subnet %s has no free addresses left for every client: widen it", e.Tag, e.Subnet)
}

// lastAddr is the broadcast address of a prefix.
func lastAddr(p netip.Prefix) netip.Addr {
	a := p.Masked().Addr().As4()
	host := 32 - p.Bits()
	v := uint32(a[0])<<24 | uint32(a[1])<<16 | uint32(a[2])<<8 | uint32(a[3])
	v |= (1 << host) - 1
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

// PeerFor returns the peer of a client on an inbound, creating it when the
// client is bound and has none yet. ok is false when the client is not bound.
func PeerFor(db *gorm.DB, in Inbound, clientID uint) (Peer, bool, error) {
	peers, err := SyncPeers(db, in)
	var full *PoolFullError
	if err != nil && !errors.As(err, &full) {
		return Peer{}, false, err
	}
	for _, p := range peers {
		if p.ClientID == clientID && p.Bound {
			return p, true, nil
		}
	}
	// Bound but without an address: the pool is full.
	if full != nil {
		return Peer{}, false, full
	}
	return Peer{}, false, nil
}

// DeletePeers removes every peer row of an inbound (the inbound was deleted).
func DeletePeers(db *gorm.DB, inboundID uint) error {
	return db.Where("inbound_id = ?", inboundID).Delete(&model.WgPeer{}).Error
}

// Active filters the peers the server should accept: bound and enabled.
func Active(peers []Peer) []Peer {
	out := make([]Peer, 0, len(peers))
	for _, p := range peers {
		if p.Bound && p.ClientEnable {
			out = append(out, p)
		}
	}
	return out
}

// DB is the panel's database handle.
func DB() *gorm.DB { return database.GetDB() }

// outboundTagExists refuses a forced outbound that the panel does not have.
// An empty tag means "by routing rules" and is fine; a typo used to be saved
// and then silently dropped traffic once the core TUN was up.
func outboundTagExists(tx *gorm.DB, tag string) error {
	var n int64
	if err := tx.Table("outbounds").Where("tag = ?", tag).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("outbound %q does not exist: pick one from Outbounds, or leave it empty for routing rules", tag)
	}
	return nil
}
