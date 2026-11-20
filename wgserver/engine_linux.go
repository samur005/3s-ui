//go:build linux

package wgserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/amnezia-vpn/amneziawg-go/v3/conn"
	"github.com/amnezia-vpn/amneziawg-go/v3/device"
	"github.com/amnezia-vpn/amneziawg-go/v3/tun"
	"github.com/samur005/3s-ui/logger"
)

// EngineSupported is true where the server device can be created.
func EngineSupported() bool { return true }

// RoutingSupported is true on Linux, where policy routing is available.
func RoutingSupported() bool { return true }

// Manager owns the running devices, one per enabled inbound.
type Manager struct {
	mu   sync.Mutex
	devs map[uint]*running
	errs map[uint]string
}

type running struct {
	typ    string
	iface  string
	fp     string
	dev    *device.Device
	peers  map[string]string // public key -> peer fingerprint
	routed bool
	cfg    Config
}

var defaultMgr = &Manager{devs: map[uint]*running{}, errs: map[uint]string{}}

// Get returns the process-wide manager.
func Get() *Manager { return defaultMgr }

func (m *Manager) setErr(id uint, msg string) {
	if msg != "" && m.errs[id] != msg {
		logger.Warningf("wgserver: inbound %d: %s", id, msg)
	}
	if msg == "" {
		delete(m.errs, id)
		return
	}
	m.errs[id] = msg
}

// Reconcile brings the devices, peers and routing in line with the stored
// inbounds. It is cheap when nothing changed, and is called by the cron job,
// after every inbound save and on start.
func (m *Manager) Reconcile() {
	m.mu.Lock()
	defer m.mu.Unlock()
	db := DB()
	inbounds, err := LoadInbounds(db)
	if err != nil {
		logger.Warning("wgserver: reading inbounds: ", err)
		return
	}
	want := map[uint]Inbound{}
	for _, in := range inbounds {
		if in.Enable {
			want[in.ID] = in
		}
	}
	for id, r := range m.devs {
		if _, ok := want[id]; !ok {
			m.stopLocked(id, r)
			delete(m.errs, id)
		}
	}
	for id := range m.errs {
		if _, ok := want[id]; !ok {
			delete(m.errs, id)
		}
	}
	for _, in := range inbounds {
		if !in.Enable {
			continue
		}
		m.ensureLocked(in)
	}
}

// Restart rebuilds one inbound's device.
func (m *Manager) Restart(id uint) {
	m.mu.Lock()
	if r, ok := m.devs[id]; ok {
		m.stopLocked(id, r)
	}
	m.mu.Unlock()
	m.Reconcile()
}

// Shutdown stops every device and removes its routing; called on panel exit.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, r := range m.devs {
		m.stopLocked(id, r)
	}
}

func (m *Manager) stopLocked(id uint, r *running) {
	if r.dev != nil {
		r.dev.Close()
	}
	clearNetwork(id, r.iface, r.cfg)
	delete(m.devs, id)
}

func (m *Manager) ensureLocked(in Inbound) {
	if err := validForRun(in); err != nil {
		m.setErr(in.ID, err.Error())
		return
	}
	peers, err := SyncPeers(DB(), in)
	var full *PoolFullError
	if err != nil && !errors.As(err, &full) {
		m.setErr(in.ID, "peers: "+err.Error())
		return
	}
	active := Active(peers)
	fp := deviceFingerprint(in.Type, in.Config)
	r := m.devs[in.ID]
	if r != nil && r.fp != fp {
		m.stopLocked(in.ID, r)
		r = nil
	}
	if r == nil {
		nr, err := startDevice(in)
		if err != nil {
			m.setErr(in.ID, err.Error())
			return
		}
		r = nr
		m.devs[in.ID] = r
	}
	r.cfg = in.Config
	if err := syncDevicePeers(r, active); err != nil {
		m.setErr(in.ID, "peers: "+err.Error())
		return
	}
	// Routing: through the core's TUN, or straight out with NAT.
	if in.Config.RouteThroughCore {
		r.routed = ensureCoreRouting(in, r.iface)
		if !r.routed {
			m.setErr(in.ID, "waiting for the core's TUN interface "+TunName(in.ID)+": the core has to be running with this inbound")
			return
		}
	} else {
		r.routed = false
		if err := ensureNAT(in, r.iface); err != nil {
			m.setErr(in.ID, err.Error())
			return
		}
	}
	if full != nil {
		m.setErr(in.ID, full.Error())
		return
	}
	m.setErr(in.ID, "")
}

func validForRun(in Inbound) error {
	if _, err := in.Config.ServerPrefix(); err != nil {
		return err
	}
	if in.Config.ListenPort < 1 || in.Config.ListenPort > 65535 {
		return fmt.Errorf("invalid port")
	}
	return nil
}

func engineLogger(id uint) *device.Logger {
	return &device.Logger{
		Verbosef: func(string, ...any) {},
		Errorf: func(format string, args ...any) {
			msg := fmt.Sprintf(format, args...)
			// A peer that never connected (or has gone) has no endpoint to send
			// to; the engine says so on every timer tick, which is not news.
			if strings.Contains(msg, "no known endpoint for peer") {
				return
			}
			logger.Warningf("wgserver: inbound %d: %s", id, msg)
		},
	}
}

func startDevice(in Inbound) (*running, error) {
	conf, err := deviceConfig(in.Type, in.Config)
	if err != nil {
		return nil, err
	}
	iface := IfaceName(in.Type, in.ID)
	tdev, err := tun.CreateTUN(iface, in.Config.EffectiveMTU(in.Type))
	if err != nil {
		return nil, fmt.Errorf("creating the network device %s: %w (needs root and /dev/net/tun)", iface, err)
	}
	dev := device.NewDevice(tdev, conn.NewDefaultBind(), engineLogger(in.ID))
	if err := dev.IpcSet(conf); err != nil {
		dev.Close()
		return nil, fmt.Errorf("configuring the server: %w", err)
	}
	if err := dev.Up(); err != nil {
		dev.Close()
		return nil, fmt.Errorf("bringing the server up: %w", err)
	}
	prefix, _ := in.Config.ServerPrefix()
	if err := ipRun("addr", "replace", prefix.String(), "dev", iface); err != nil {
		dev.Close()
		return nil, fmt.Errorf("addressing %s: %w", iface, err)
	}
	if err := ipRun("link", "set", "dev", iface, "mtu", strconv.Itoa(in.Config.EffectiveMTU(in.Type)), "up"); err != nil {
		dev.Close()
		return nil, fmt.Errorf("raising %s: %w", iface, err)
	}
	logger.Infof("wgserver: %s %q listening on UDP %d (%s)", in.Type, in.Tag, in.Config.ListenPort, iface)
	return &running{typ: in.Type, iface: iface, fp: deviceFingerprint(in.Type, in.Config), dev: dev, peers: map[string]string{}, cfg: in.Config}, nil
}

func peerFP(p Peer) string { return p.PresharedKey + "|" + p.Address }

// syncDevicePeers changes only what differs: peers that left are removed,
// new or changed ones are set. Peers that stay keep their sessions.
func syncDevicePeers(r *running, active []Peer) error {
	desired := map[string]Peer{}
	for _, p := range active {
		desired[p.PublicKey] = p
	}
	var b strings.Builder
	for pub := range r.peers {
		if _, ok := desired[pub]; !ok {
			s, err := peerRemove(pub)
			if err != nil {
				return err
			}
			b.WriteString(s)
		}
	}
	for pub, p := range desired {
		if r.peers[pub] == peerFP(p) {
			continue
		}
		s, err := peerAdd(p)
		if err != nil {
			return err
		}
		b.WriteString(s)
	}
	if b.Len() == 0 {
		return nil
	}
	if err := r.dev.IpcSet(b.String()); err != nil {
		return err
	}
	r.peers = map[string]string{}
	for pub, p := range desired {
		r.peers[pub] = peerFP(p)
	}
	return nil
}

// Stats returns the engine's per-peer counters for an inbound.
func (m *Manager) Stats(id uint) []PeerStat {
	m.mu.Lock()
	r := m.devs[id]
	m.mu.Unlock()
	if r == nil || r.dev == nil {
		return nil
	}
	text, err := r.dev.IpcGet()
	if err != nil {
		return nil
	}
	return parseStats(text)
}

// Status snapshots one inbound.
func (m *Manager) Status(in Inbound) Status {
	m.mu.Lock()
	r := m.devs[in.ID]
	errMsg := m.errs[in.ID]
	st := Status{Supported: true, Iface: IfaceName(in.Type, in.ID), Error: errMsg}
	if r != nil {
		st.Running = true
		st.Routed = r.routed
		st.Peers = len(r.peers)
	}
	m.mu.Unlock()
	if r != nil {
		now := time.Now().Unix()
		for _, ps := range m.Stats(in.ID) {
			if ps.LastHandshake > 0 && now-ps.LastHandshake < 180 {
				st.Online++
			}
		}
	}
	st.TunReady = in.Config.RouteThroughCore && linkExists(TunName(in.ID))
	return st
}

// ---- host networking ----

func ipRun(args ...string) error {
	out, err := exec.CommandContext(context.Background(), "ip", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("ip %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func linkExists(name string) bool {
	return exec.CommandContext(context.Background(), "ip", "link", "show", name).Run() == nil
}

func runQuiet(name string, args ...string) {
	if out, err := exec.CommandContext(context.Background(), name, args...).CombinedOutput(); err != nil {
		logger.Warningf("wgserver: %s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
}

func succeeds(name string, args ...string) bool {
	return exec.CommandContext(context.Background(), name, args...).Run() == nil
}

// ensureIptables adds a rule once: -C checks, -A/-I installs.
func ensureIptables(table string, insert bool, chain string, rule ...string) {
	base := []string{}
	if table != "" {
		base = append(base, "-t", table)
	}
	if !haveIptables() {
		return
	}
	if succeeds("iptables", append(append(append([]string{}, base...), "-C", chain), rule...)...) {
		return
	}
	op := []string{"-A", chain}
	if insert {
		op = []string{"-I", chain, "1"}
	}
	runQuiet("iptables", append(append(append([]string{}, base...), op...), rule...)...)
}

func deleteIptables(table, chain string, rule ...string) {
	base := []string{}
	if table != "" {
		base = append(base, "-t", table)
	}
	for i := 0; i < 4; i++ {
		if !succeeds("iptables", append(append(append([]string{}, base...), "-D", chain), rule...)...) {
			return
		}
	}
}

// ensureForward lets the inbound's traffic through the host's FORWARD chain
// (distributions and firewalls such as ufw drop it by default) and turns
// forwarding on.
func ensureForward(iface string) {
	writeProc("net/ipv4/ip_forward", "1")
	if !haveIptables() {
		return
	}
	ensureIptables("", true, "FORWARD", "-i", iface, "-j", "ACCEPT")
	ensureIptables("", true, "FORWARD", "-o", iface, "-j", "ACCEPT")
}

func haveIptables() bool {
	_, err := exec.LookPath("iptables")
	return err == nil
}

// writeProc sets a kernel parameter through /proc/sys directly: minimal
// systems have no sysctl binary.
func writeProc(path, value string) {
	if err := os.WriteFile("/proc/sys/"+path, []byte(value), 0o644); err != nil {
		logger.Warningf("wgserver: setting %s: %v", path, err)
	}
}

// ensureNAT lets the inbound's clients out by masquerading their subnet. It
// reports an error when the host cannot do that (no iptables), since without
// it the clients would connect and then reach nothing.
func ensureNAT(in Inbound, iface string) error {
	clearCoreRouting(in.ID, iface)
	if !haveIptables() {
		return fmt.Errorf("iptables is not installed: the panel needs it to forward and NAT client traffic (apt install iptables), or route the inbound through the core")
	}
	ensureForward(iface)
	ensureIptables("nat", false, "POSTROUTING", "-s", in.Config.Subnet, "!", "-o", iface, "-j", "MASQUERADE")
	return nil
}

// ensureCoreRouting sends the inbound's client traffic into the core's TUN: a
// default route in a private table and an "iif <iface> lookup <table>" rule. It
// reports whether the routing is complete (the TUN exists).
func ensureCoreRouting(in Inbound, iface string) bool {
	tun := TunName(in.ID)
	if !linkExists(tun) || !linkExists(iface) {
		return false
	}
	table := strconv.Itoa(RouteTable(in.ID))
	runQuiet("ip", "route", "replace", "default", "dev", tun, "table", table)
	// Loose reverse-path filtering: replies come back on the TUN, not on the
	// interface the request arrived on.
	writeProc("net/ipv4/conf/"+tun+"/rp_filter", "2")
	writeProc("net/ipv4/conf/"+iface+"/rp_filter", "2")
	out, err := exec.CommandContext(context.Background(), "ip", "rule", "show", "iif", iface).Output()
	if err != nil || !strings.Contains(string(out), "lookup "+table) {
		runQuiet("ip", "rule", "add", "iif", iface, "lookup", table)
	}
	ensureForward(iface)
	// The core NATs on the way out; a masquerade here would hide the client's
	// address from the core's rules.
	deleteIptables("nat", "POSTROUTING", "-s", in.Config.Subnet, "!", "-o", iface, "-j", "MASQUERADE")
	return true
}

func clearCoreRouting(id uint, iface string) {
	table := strconv.Itoa(RouteTable(id))
	for i := 0; i < 4; i++ {
		if !succeeds("ip", "rule", "del", "iif", iface, "lookup", table) {
			break
		}
	}
	_ = exec.CommandContext(context.Background(), "ip", "route", "flush", "table", table).Run()
}

func clearNetwork(id uint, iface string, cfg Config) {
	clearCoreRouting(id, iface)
	deleteIptables("", "FORWARD", "-i", iface, "-j", "ACCEPT")
	deleteIptables("", "FORWARD", "-o", iface, "-j", "ACCEPT")
	if cfg.Subnet != "" {
		deleteIptables("nat", "POSTROUTING", "-s", cfg.Subnet, "!", "-o", iface, "-j", "MASQUERADE")
	}
}
