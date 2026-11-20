// Package wgserver runs WireGuard and AmneziaWG servers as panel inbounds.
//
// Both protocols share one userspace engine (amneziawg-go): with no
// obfuscation parameters it speaks plain WireGuard, so a "wg" inbound and an
// "awg" inbound differ only in the options they carry. The panel owns the
// network device of each inbound, hands every bound client a peer (keys and an
// address from the inbound's pool) and, when asked, sends the decrypted client
// traffic into a sing-box TUN inbound so the panel's routing rules and
// outbounds apply to it.
//
// The pieces that need an operating system (device, routes, firewall) live in
// linux-only files; everything else -- options, validation, parameter
// generation, client configs and share links -- is portable and unit-tested.
package wgserver

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
)

// Inbound types handled by this package.
const (
	TypeWG  = "wg"
	TypeAWG = "awg"
)

// IsType reports whether t is one of the types this package serves.
func IsType(t string) bool { return t == TypeWG || t == TypeAWG }

// Types lists them, in a stable order.
func Types() []string { return []string{TypeWG, TypeAWG} }

// Defaults.
const (
	DefaultPort      = 51820
	DefaultMTU       = 1420
	DefaultKeepalive = 25
	DefaultDNS       = "1.1.1.1, 8.8.8.8"
	DefaultAllowed   = "0.0.0.0/0, ::/0"
	// DefaultAWGVersion is the widest-supported generation.
	DefaultAWGVersion = "2.0"
)

// Config is the stored shape of a WireGuard/AmneziaWG inbound's options. The
// JSON keys are what the inbound form sends and reads.
type Config struct {
	// ListenPort is the UDP port the server listens on. Stored under the
	// sing-box-style key "listen_port" so the Inbounds list can show it.
	ListenPort int `json:"listen_port"`
	// Listen is informational ("::"): the engine always binds every address.
	Listen string `json:"listen,omitempty"`

	PrivateKey string `json:"privateKey"`
	// PublicKey is derived from PrivateKey and stored only so the form can show
	// it; it is recomputed on every save.
	PublicKey string `json:"publicKey"`

	// Subnet is the tunnel network, e.g. 10.8.1.0/24. The server takes the first
	// host address, clients the following ones.
	Subnet    string `json:"subnet"`
	MTU       int    `json:"mtu"`
	DNS       string `json:"dns"`
	Keepalive int    `json:"keepalive"`
	// AllowedIPs is what client configs route into the tunnel.
	AllowedIPs string `json:"allowedIps"`
	// Endpoint is the host (or host:port) client configs dial. Empty means the
	// subscription domain, the panel domain or the server's outbound address.
	Endpoint string `json:"endpoint"`

	// RouteThroughCore sends client traffic through a sing-box TUN inbound so
	// the panel's routing rules apply; OutboundTag pins it to one outbound.
	RouteThroughCore bool   `json:"routeThroughCore"`
	OutboundTag      string `json:"outboundTag"`

	// Version selects the AmneziaWG generation ("1.0", "1.5", "2.0", "2.5",
	// "3.0", "3.1") and with it which obfuscation fields exist. Empty for wg.
	Version string `json:"version,omitempty"`
	Obfuscation
}

// Obfuscation is the AmneziaWG parameter set across all generations. A field
// only means something for the versions that have it (see Fields); the others
// are dropped when the options are normalised.
type Obfuscation struct {
	Jc   int `json:"jc"`
	Jmin int `json:"jmin"`
	Jmax int `json:"jmax"`
	S1   int `json:"s1"`
	S2   int `json:"s2"`
	S3   int `json:"s3"`
	S4   int `json:"s4"`
	// H1-H4 are single numbers up to 1.5 and "low-high" ranges from 2.0.
	H1 string `json:"h1"`
	H2 string `json:"h2"`
	H3 string `json:"h3"`
	H4 string `json:"h4"`
	// I1-I5 are the CPS signature-packet chains (1.5+), client-side.
	I1 string `json:"i1"`
	I2 string `json:"i2"`
	I3 string `json:"i3"`
	I4 string `json:"i4"`
	I5 string `json:"i5"`
	// 3.0+: header protection and transport padding/timing.
	HeaderProtectionKey    string `json:"headerProtectionKey"`
	ContentPaddingAddition string `json:"contentPaddingAddition"`
	RekeyAfterTime         string `json:"rekeyAfterTime"`
	RekeyTimeout           string `json:"rekeyTimeout"`
	RejectAfterTime        string `json:"rejectAfterTime"`
	KeepaliveTimeout       string `json:"keepaliveTimeout"`
	MaxHandshakeAttempts   string `json:"maxHandshakeAttempts"`
	// 3.1+.
	RandomTrailers bool `json:"randomTrailers"`
	DisableCookies bool `json:"disableCookies"`
}

// Decode reads a stored options document.
func Decode(raw json.RawMessage) (Config, error) {
	var c Config
	if len(raw) == 0 || string(raw) == "null" {
		return c, nil
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("invalid options: %w", err)
	}
	return c, nil
}

// Encode is the stored shape.
func (c Config) Encode() (json.RawMessage, error) {
	if c.Listen == "" {
		c.Listen = "::"
	}
	return json.Marshal(c)
}

// ServerAddr is the server's own tunnel address (first host of the subnet).
func (c Config) ServerAddr() (netip.Addr, error) {
	p, err := netip.ParsePrefix(c.Subnet)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("invalid subnet %q", c.Subnet)
	}
	return p.Masked().Addr().Next(), nil
}

// ServerPrefix is ServerAddr with the subnet's prefix length, as `ip addr add`
// wants it.
func (c Config) ServerPrefix() (netip.Prefix, error) {
	p, err := netip.ParsePrefix(c.Subnet)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("invalid subnet %q", c.Subnet)
	}
	a, err := c.ServerAddr()
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(a, p.Bits()), nil
}

// EffectiveMTU is the tunnel MTU: the configured one, or the default minus the
// transport padding AmneziaWG prepends to every packet.
func (c Config) EffectiveMTU(typ string) int {
	if c.MTU > 0 {
		return c.MTU
	}
	if typ == TypeAWG {
		return max(DefaultMTU-max(c.S4, 0), 1280)
	}
	return DefaultMTU
}

// splitList splits "a, b  c" on commas and spaces.
func splitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == ';' || r == '\n' || r == '\t' })
}
