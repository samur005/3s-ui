package xray

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
)

// Client identity across the Xray → sing-box handoff:
//
// Xray can only put fixed SOCKS credentials on each socks outbound (one user
// per outbound). To let sing-box auth_user rules see which VLESS client is
// talking, we create one socks outbound per bound client and route by Xray
// routing "user" (the VLESS email, which is the panel client name). The
// matching mixed inbound lists the same usernames; passwords are derived from
// the inbound's master handoffPass so they are never put in share links.
//
// Original client IP cannot be preserved: the TCP/UDP association to sing-box
// always comes from 127.0.0.1. PROXY protocol is not available on Xray's SOCKS
// client in a way that keeps destination semantics for this handoff.

// DeriveClientPass builds a stable SOCKS password for one client name.
func DeriveClientPass(masterPass, clientName string) string {
	sum := sha256.Sum256([]byte(masterPass + "\x00" + clientName))
	return hex.EncodeToString(sum[:16])
}

// SocksOutboundTagForClient is a config-safe outbound tag for one client.
func SocksOutboundTagForClient(inboundID uint, clientName string) string {
	sum := sha256.Sum256([]byte(clientName))
	return fmt.Sprintf("s3ui-xray-core-%d-%x", inboundID, sum[:4])
}

func sanitizeSocksUser(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	// SOCKS usernames: keep printable non-space; panel client names are the
	// auth_user values operators already use on mixed/socks inbounds.
	var b strings.Builder
	for _, r := range name {
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

type clientSocks struct {
	Email    string
	Username string
	Password string
	OutTag   string
}

func clientSocksList(inboundID uint, masterPass string, clients []ClientCred) []clientSocks {
	out := make([]clientSocks, 0, len(clients))
	seen := map[string]bool{}
	for _, c := range clients {
		if !c.Enable || c.ID == "" {
			continue
		}
		user := sanitizeSocksUser(c.Email)
		if user == "" || seen[user] {
			continue
		}
		seen[user] = true
		out = append(out, clientSocks{
			Email:    c.Email,
			Username: user,
			Password: DeriveClientPass(masterPass, user),
			OutTag:   SocksOutboundTagForClient(inboundID, user),
		})
	}
	return out
}
