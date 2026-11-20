package wgserver

import (
	"fmt"
	"strings"
)

// deviceConfig renders the engine's configuration (its UAPI "set" text) for an
// inbound's own settings, without peers. Which obfuscation lines are written
// follows the version: a field the version does not have is left at the
// engine's neutral default, so one engine serves plain WireGuard and every
// AmneziaWG generation.
func deviceConfig(typ string, cfg Config) (string, error) {
	priv, err := keyHex(cfg.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("server private key: %w", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "private_key=%s\nlisten_port=%d\n", priv, cfg.ListenPort)
	if typ != TypeAWG {
		return b.String(), nil
	}
	f := Fields(cfg.Version)
	o := cfg.Obfuscation.Prune(cfg.Version)
	if err := o.Validate(cfg.Version); err != nil {
		return "", err
	}
	fmt.Fprintf(&b, "jc=%d\njmin=%d\njmax=%d\ns1=%d\ns2=%d\n", o.Jc, o.Jmin, o.Jmax, o.S1, o.S2)
	if f.S34 {
		fmt.Fprintf(&b, "s3=%d\ns4=%d\n", o.S3, o.S4)
	}
	opt := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "%s=%s\n", k, v)
		}
	}
	opt("h1", o.H1)
	opt("h2", o.H2)
	opt("h3", o.H3)
	opt("h4", o.H4)
	if f.CPS {
		opt("i1", o.I1)
		opt("i2", o.I2)
		opt("i3", o.I3)
		opt("i4", o.I4)
		opt("i5", o.I5)
	}
	if f.Protection {
		hpk, err := keyHex(o.HeaderProtectionKey)
		if err != nil {
			return "", fmt.Errorf("header protection key: %w", err)
		}
		fmt.Fprintf(&b, "header_protection_key=%s\n", hpk)
		opt("content_padding_addition", o.ContentPaddingAddition)
		opt("rekey_after_time", o.RekeyAfterTime)
		opt("rekey_timeout", o.RekeyTimeout)
		opt("reject_after_time", o.RejectAfterTime)
		opt("keepalive_timeout", o.KeepaliveTimeout)
		opt("max_handshake_attempts", o.MaxHandshakeAttempts)
	}
	if f.TrailersCookie {
		fmt.Fprintf(&b, "random_trailers=%t\ndisable_cookies=%t\n", o.RandomTrailers, o.DisableCookies)
	}
	return b.String(), nil
}

// peerAdd renders one peer for the engine.
func peerAdd(p Peer) (string, error) {
	pub, err := keyHex(p.PublicKey)
	if err != nil {
		return "", fmt.Errorf("peer %q: %w", p.ClientName, err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "public_key=%s\n", pub)
	if p.PresharedKey != "" {
		psk, err := keyHex(p.PresharedKey)
		if err != nil {
			return "", fmt.Errorf("peer %q: %w", p.ClientName, err)
		}
		fmt.Fprintf(&b, "preshared_key=%s\n", psk)
	}
	fmt.Fprintf(&b, "replace_allowed_ips=true\nallowed_ip=%s/32\n", p.Address)
	return b.String(), nil
}

// peerRemove renders the removal of one peer.
func peerRemove(publicKey string) (string, error) {
	pub, err := keyHex(publicKey)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("public_key=%s\nremove=true\n", pub), nil
}

// deviceFingerprint changes whenever the device itself has to be rebuilt (as
// opposed to just its peers being updated).
func deviceFingerprint(typ string, cfg Config) string {
	cfg.Endpoint, cfg.DNS, cfg.Keepalive, cfg.AllowedIPs = "", "", 0, ""
	cfg.RouteThroughCore, cfg.OutboundTag = false, ""
	cfg.PublicKey = ""
	enc, _ := cfg.Encode()
	return typ + "|" + string(enc) + fmt.Sprint(cfg.EffectiveMTU(typ))
}
