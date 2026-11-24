// Package qwdtt integrates the qWDTT server (WireGuard over DTLS over VK call
// TURN relays; SpaceNeuroX/proxy-turn-vk-android) into 3S-UI.
//
// The server is a separate binary driven purely by command-line flags. This
// package supervises it, keeps its settings in the panel database, steers its
// client traffic into a sing-box TUN inbound so the panel's own routing rules
// and outbounds apply, creates the VK call hashes the clients dial through and
// renders the qwdtt:// share links.
package qwdtt

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/samur005/3s-ui/config"
)

// EnvVkHash overrides the VK hash list. While it is set the stored hashes are
// ignored and no hash is generated or replaced automatically.
const EnvVkHash = "S3UI_VK_HASH"

// Config is the operator-facing qWDTT configuration, persisted as JSON.
type Config struct {
	Remark  string `json:"remark"`
	Enabled bool   `json:"enabled"`

	// ListenAddr is the DTLS entry point clients connect to, "0.0.0.0:56000".
	ListenAddr string `json:"listenAddr"`
	// WGPort is the server's internal userspace WireGuard UDP port.
	WGPort int `json:"wgPort"`
	// Password is the shared secret clients present. Generated when empty.
	Password string `json:"password"`
	// DNS is pushed to clients: a bare IP, no port.
	DNS string `json:"dns"`
	// ConfigDir holds the server's own state (passwords.json, wg-keys.dat).
	// Empty means <db folder>/qwdtt.
	ConfigDir string `json:"configDir"`
	// ListenRaw is the optional raw-IP listener (no WireGuard).
	ListenRaw string `json:"listenRaw"`
	// ListenDirect is the optional direct RTP-obfuscated listener; empty = off.
	ListenDirect string `json:"listenDirect"`

	// SubHost is the "host:dtlsPort" advertised in share links. Empty means the
	// panel works it out from the request or the host's outbound address.
	SubHost string `json:"subHost"`
	// VkHashes is a comma-separated list of live VK call hashes.
	VkHashes string `json:"vkHashes"`
	// ClientPort is the local TUN port written into share links for the client.
	ClientPort int `json:"clientPort"`
	// Workers is the workers-per-hash value written into share links.
	Workers int `json:"workers"`

	// AutoVkHash lets the panel create a VK call when no hash is configured and
	// replace hashes whose call has ended. Needs VK cookies.
	AutoVkHash bool `json:"autoVkHash"`

	// UserMode decides who holds which password. "perClient" (the default for
	// a new inbound) gives every bound panel client its own password in the
	// server's passwords.json, so enabling, disabling, quota, expiry and the
	// traffic counters are per client; Password stays the owner's own. "shared"
	// hands the one Password to everyone, as before; rows stored before the key
	// existed read as shared so their links keep working.
	UserMode string `json:"userMode"`
	// MaxDevices is how many devices one per-client password may bind on the
	// server (a client's IP limit, when set, wins). 0 means DefaultMaxDevices.
	MaxDevices int `json:"maxDevices"`

	// RouteThroughCore steers wdtt0/wdttraw0 into a sing-box TUN inbound the
	// panel adds to the core config, so routing rules and outbounds apply.
	RouteThroughCore bool `json:"routeThroughCore"`
	// OutboundTag, when set, forces every qWDTT client through that outbound.
	OutboundTag string `json:"outboundTag"`
}

// User modes.
const (
	UserModeShared    = "shared"
	UserModePerClient = "perClient"
)

// DefaultMaxDevices is the per-password device cap when none is set.
const DefaultMaxDevices = 3

// PerClient reports whether every client gets a password of its own.
func (c Config) PerClient() bool { return c.UserMode == UserModePerClient }

// Default returns the configuration of a fresh install.
func Default() Config {
	return Config{
		ListenAddr:       "0.0.0.0:56000",
		WGPort:           56001,
		DNS:              "8.8.8.8",
		ListenRaw:        "0.0.0.0:56003",
		ClientPort:       9000,
		Workers:          16,
		AutoVkHash:       true,
		RouteThroughCore: true,
		UserMode:         UserModePerClient,
		MaxDevices:       DefaultMaxDevices,
	}
}

// Merge fills zero fields from the defaults.
func (c Config) Merge() Config {
	d := Default()
	if c.ListenAddr == "" {
		c.ListenAddr = d.ListenAddr
	}
	if c.WGPort == 0 {
		c.WGPort = d.WGPort
	}
	if c.DNS == "" {
		c.DNS = d.DNS
	}
	if c.ClientPort == 0 {
		c.ClientPort = d.ClientPort
	}
	if c.Workers == 0 {
		c.Workers = d.Workers
	}
	if c.UserMode == "" {
		c.UserMode = d.UserMode
	}
	if c.MaxDevices == 0 {
		c.MaxDevices = d.MaxDevices
	}
	return c
}

// Validate checks the configuration for the flags the server takes.
func (c Config) Validate() error {
	if _, port, err := net.SplitHostPort(c.ListenAddr); err != nil || port == "" {
		return errors.New("qwdtt: listenAddr must be host:port (e.g. 0.0.0.0:56000)")
	}
	if c.WGPort < 1 || c.WGPort > 65535 {
		return errors.New("qwdtt: wgPort must be in 1..65535")
	}
	dns := strings.TrimSpace(c.DNS)
	if dns == "" {
		return errors.New("qwdtt: dns is required")
	}
	if strings.Contains(dns, ":") {
		if _, _, err := net.SplitHostPort(dns); err == nil {
			return errors.New("qwdtt: dns must be a bare host/IP (no port), e.g. 8.8.8.8")
		}
	}
	for name, v := range map[string]string{"listenRaw": c.ListenRaw, "listenDirect": c.ListenDirect} {
		if v = strings.TrimSpace(v); v != "" {
			if _, _, err := net.SplitHostPort(v); err != nil {
				return fmt.Errorf("qwdtt: %s must be host:port or empty", name)
			}
		}
	}
	if sh := strings.TrimSpace(c.SubHost); sh != "" {
		if _, _, err := net.SplitHostPort(sh); err != nil {
			return errors.New("qwdtt: subHost must be host:port or empty")
		}
	}
	if c.ClientPort < 1 || c.ClientPort > 65535 {
		return errors.New("qwdtt: clientPort must be in 1..65535")
	}
	if c.Workers < 1 || c.Workers > 64 {
		return errors.New("qwdtt: workers must be in 1..64")
	}
	switch c.UserMode {
	case "", UserModeShared, UserModePerClient:
	default:
		return errors.New("qwdtt: userMode must be shared or perClient")
	}
	if c.MaxDevices < 0 || c.MaxDevices > 100 {
		return errors.New("qwdtt: maxDevices must be in 0..100")
	}
	if tag := strings.TrimSpace(c.OutboundTag); strings.ContainsAny(tag, "\r\n\t") {
		return errors.New("qwdtt: outboundTag has invalid characters")
	}
	return nil
}

// EnsurePassword returns c with a generated password when it has none.
func (c Config) EnsurePassword() (Config, error) {
	if strings.TrimSpace(c.Password) != "" {
		return c, nil
	}
	p, err := generatePassword(16)
	if err != nil {
		return c, err
	}
	c.Password = p
	return c, nil
}

// generatePassword draws n characters from an alphabet without the look-alikes
// 0/O/1/l. Rejection sampling keeps the draw uniform.
func generatePassword(n int) (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789"
	limit := 256 - 256%len(alphabet)
	out := make([]byte, 0, n)
	buf := make([]byte, n*2)
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			if int(b) >= limit {
				continue
			}
			out = append(out, alphabet[int(b)%len(alphabet)])
			if len(out) == n {
				break
			}
		}
	}
	return string(out), nil
}

// DTLSPort is the port of ListenAddr, 56000 when it cannot be read.
func (c Config) DTLSPort() int {
	if _, port, err := net.SplitHostPort(c.ListenAddr); err == nil {
		if p, err := strconv.Atoi(port); err == nil && p > 0 {
			return p
		}
	}
	return 56000
}

// WithPeerHost sets SubHost to host:dtlsPort. An empty host changes nothing.
func (c Config) WithPeerHost(host string) Config {
	host = strings.TrimSpace(host)
	if host == "" {
		return c
	}
	c.SubHost = net.JoinHostPort(host, strconv.Itoa(c.DTLSPort()))
	return c
}

// ResolveConfigDir is the directory the server keeps its state in.
func (c Config) ResolveConfigDir() string {
	if d := strings.TrimSpace(c.ConfigDir); d != "" {
		if abs, err := filepath.Abs(d); err == nil {
			return abs
		}
	}
	return filepath.Join(config.GetDBFolderPath(), "qwdtt")
}

// BuildArgs is the argv for the server binary: flags only, no subcommand.
func (c Config) BuildArgs() []string {
	args := []string{
		"-listen", c.ListenAddr,
		"-wg-port", strconv.Itoa(c.WGPort),
		"-config-dir", c.ResolveConfigDir(),
	}
	if p := strings.TrimSpace(c.Password); p != "" {
		args = append(args, "-password", p)
	}
	if d := strings.TrimSpace(c.DNS); d != "" {
		args = append(args, "-dns", d)
	}
	if v := strings.TrimSpace(c.ListenRaw); v != "" {
		args = append(args, "-listen-raw", v)
	}
	if v := strings.TrimSpace(c.ListenDirect); v != "" {
		args = append(args, "-listen-direct", v)
	}
	return args
}

// EffectiveHashes is the hash list clients are given: the environment override
// when set, else the stored list.
func (c Config) EffectiveHashes() string {
	if h := EnvHashOverride(); h != "" {
		return h
	}
	return NormalizeHashes(c.VkHashes)
}

// EnvHashOverride returns the normalized S3UI_VK_HASH value, "" when unset.
func EnvHashOverride() string {
	return NormalizeHashes(os.Getenv(EnvVkHash))
}

// ClientURI renders the qwdtt://config?... share link with the shared
// password (the owner's link in per-client mode). It is one line: a second line
// would be parsed as part of the password and the DTLS handshake would fail.
// Empty peer or password gives "".
func (c Config) ClientURI() string {
	return c.ClientURIFor(c.Password, "")
}

// LinkParams is everything one qwdtt:// link says.
type LinkParams struct {
	Name     string
	Peer     string
	Hashes   string
	Workers  int
	Port     int
	Password string
	// DiagKey (dk), RawPort (raw) and DirectPort (direct) are what newer
	// qWDTT apps read for the connection check and the extra listeners; older
	// apps ignore them.
	DiagKey    string
	RawPort    int
	DirectPort int
}

// Params is the link of this configuration for one password, with diagKey
// passed in (the server keeps it in its state directory, see DiagKey).
func (c Config) Params(password, diagKey string) LinkParams {
	name := strings.TrimSpace(c.Remark)
	if name == "" {
		name = "qWDTT"
	}
	return LinkParams{
		Name:       name,
		Peer:       strings.TrimSpace(c.SubHost),
		Hashes:     c.EffectiveHashes(),
		Workers:    c.Workers,
		Port:       c.ClientPort,
		Password:   strings.TrimSpace(password),
		DiagKey:    diagKey,
		RawPort:    portOf(c.ListenRaw),
		DirectPort: portOf(c.ListenDirect),
	}
}

// ClientURIFor is the link for one password ("" when peer or password is
// missing).
func (c Config) ClientURIFor(password, diagKey string) string {
	return c.Params(password, diagKey).URI()
}

// URI renders the link, keys in the order the qWDTT server's own links use
// (server/bot_links.go): name, peer, hashes, workers, port, pass, dk, raw,
// direct.
func (p LinkParams) URI() string {
	if strings.TrimSpace(p.Peer) == "" || strings.TrimSpace(p.Password) == "" {
		return ""
	}
	type kv struct{ k, v string }
	pairs := []kv{
		{"name", p.Name}, {"peer", p.Peer}, {"hashes", p.Hashes},
		{"workers", strconv.Itoa(p.Workers)}, {"port", strconv.Itoa(p.Port)},
		{"pass", p.Password}, {"dk", p.DiagKey},
	}
	if p.RawPort > 0 {
		pairs = append(pairs, kv{"raw", strconv.Itoa(p.RawPort)})
	}
	if p.DirectPort > 0 {
		pairs = append(pairs, kv{"direct", strconv.Itoa(p.DirectPort)})
	}
	var b strings.Builder
	b.WriteString("qwdtt://config?")
	first := true
	for _, e := range pairs {
		if e.v == "" {
			continue
		}
		if !first {
			b.WriteByte('&')
		}
		first = false
		b.WriteString(e.k)
		b.WriteByte('=')
		b.WriteString(url.QueryEscape(e.v))
	}
	return b.String()
}

// ParseURI reads a qwdtt://config?... link back into its parameters (the
// subscription JSON is built from the links a client is given). ok is false
// for anything else.
func ParseURI(link string) (LinkParams, bool) {
	link = strings.TrimSpace(link)
	rest, found := strings.CutPrefix(link, "qwdtt://config?")
	if !found {
		rest, found = strings.CutPrefix(link, "qwdtt:config?")
	}
	if !found {
		return LinkParams{}, false
	}
	q, err := url.ParseQuery(rest)
	if err != nil {
		return LinkParams{}, false
	}
	p := LinkParams{
		Name: q.Get("name"), Peer: q.Get("peer"), Hashes: q.Get("hashes"),
		Password: q.Get("pass"), DiagKey: q.Get("dk"),
	}
	if p.Password == "" {
		p.Password = q.Get("password")
	}
	p.Workers, _ = strconv.Atoi(q.Get("workers"))
	p.Port, _ = strconv.Atoi(q.Get("port"))
	p.RawPort, _ = strconv.Atoi(q.Get("raw"))
	p.DirectPort, _ = strconv.Atoi(q.Get("direct"))
	if p.Peer == "" || p.Password == "" {
		return LinkParams{}, false
	}
	return p, true
}

// portOf is the port of a host:port, 0 when there is none.
func portOf(addr string) int {
	if _, port, err := net.SplitHostPort(strings.TrimSpace(addr)); err == nil {
		if p, err := strconv.Atoi(port); err == nil && p > 0 {
			return p
		}
	}
	return 0
}

// SubProfile is one profile of the qWDTT subscription JSON document.
type SubProfile struct {
	Name       string `json:"name"`
	Peer       string `json:"peer"`
	Hashes     string `json:"hashes"`
	Workers    int    `json:"workers"`
	Port       int    `json:"port"`
	Password   string `json:"password"`
	DiagKey    string `json:"diagKey,omitempty"`
	RawPort    int    `json:"rawPort,omitempty"`
	DirectPort int    `json:"directPort,omitempty"`
}

// Profile is the subscription entry of one link.
func (p LinkParams) Profile() SubProfile {
	return SubProfile{
		Name: p.Name, Peer: p.Peer, Hashes: p.Hashes, Workers: p.Workers,
		Port: p.Port, Password: p.Password, DiagKey: p.DiagKey,
		RawPort: p.RawPort, DirectPort: p.DirectPort,
	}
}

// Subscription is the JSON document the qWDTT Android client imports.
type Subscription struct {
	SubscriptionName string       `json:"subscriptionName"`
	Description      string       `json:"description,omitempty"`
	TrafficUsedMb    float64      `json:"trafficUsedMb,omitempty"`
	TrafficLimitMb   float64      `json:"trafficLimitMb,omitempty"`
	Version          int          `json:"version"`
	UpdatedAt        string       `json:"updatedAt"`
	Profiles         []SubProfile `json:"profiles"`
}

// Subscription builds the import document; peer and password are required.
func (c Config) Subscription() (Subscription, error) {
	peer := strings.TrimSpace(c.SubHost)
	pass := strings.TrimSpace(c.Password)
	if peer == "" {
		return Subscription{}, errors.New("qwdtt: subHost is required for a subscription")
	}
	if pass == "" {
		return Subscription{}, errors.New("qwdtt: password is required for a subscription")
	}
	name := strings.TrimSpace(c.Remark)
	if name == "" {
		name = "qWDTT"
	}
	return Subscription{
		SubscriptionName: name,
		Description:      "qWDTT tunnel via " + peer,
		Version:          1,
		UpdatedAt:        time.Now().Format("2006-01-02"),
		Profiles: []SubProfile{{
			Name: name, Peer: peer, Hashes: c.EffectiveHashes(),
			Workers: c.Workers, Port: c.ClientPort, Password: pass,
		}},
	}, nil
}

func (c Config) encode() string {
	b, _ := json.Marshal(c)
	return string(b)
}
