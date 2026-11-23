package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/samur005/3s-ui/util/common"
)

// Network policy for master -> node calls.
//
// The master dials whatever address an operator typed into the Nodes page, with
// an API token attached. Two things must not follow from that: the panel being
// used to probe its own infrastructure (cloud metadata at 169.254.169.254, the
// loopback admin port of something else), and the token travelling to a host
// the operator never configured. So every node request goes through a dialer
// that checks the *resolved* address -- a hostname that resolves to a
// forbidden range is refused the same as the literal -- and through a redirect
// policy that never leaves the configured host.

// errNodeAddressRefused is wrapped into every refusal so tests and callers can
// tell a policy refusal from a network failure.
var errNodeAddressRefused = errors.New("node address refused by the SSRF policy")

var cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10")

// checkNodeIP applies the address policy to one resolved IP.
//
// Always refused: unspecified, multicast and link-local (169.254.0.0/16 and
// fe80::/10 -- where cloud metadata services answer). Refused unless the node
// allows private addresses: loopback, RFC 1918 / ULA private ranges and the
// CGNAT range Tailscale-style meshes use.
func checkNodeIP(ip netip.Addr, allowPrivate bool) error {
	ip = ip.Unmap()
	if !ip.IsValid() {
		return common.NewErrorf("%v: invalid address", errNodeAddressRefused)
	}
	if ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() {
		return common.NewErrorf("%v: %s is a link-local, multicast or unspecified address", errNodeAddressRefused, ip)
	}
	if !allowPrivate && (ip.IsLoopback() || ip.IsPrivate() || cgnatPrefix.Contains(ip)) {
		return common.NewErrorf("%v: %s is a loopback or private address -- enable \"Allow private address\" on this node if that is intended", errNodeAddressRefused, ip)
	}
	return nil
}

// nodeResolver is swappable in tests.
var nodeResolver = net.DefaultResolver

// guardedDialContext resolves the host itself, checks every address, and dials
// only checked ones. Dialing the checked IP (not the name again) closes the
// DNS-rebinding window between check and connect. TLS still verifies against
// the hostname: http.Transport derives ServerName from the URL, not from here.
func guardedDialContext(allowPrivate bool, timeout time.Duration) func(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		var ips []netip.Addr
		if ip, perr := netip.ParseAddr(host); perr == nil {
			ips = []netip.Addr{ip}
		} else {
			ips, err = nodeResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
		}
		if len(ips) == 0 {
			return nil, common.NewErrorf("no address for %s", host)
		}
		var lastErr error
		for _, ip := range ips {
			if err := checkNodeIP(ip, allowPrivate); err != nil {
				// One forbidden answer refuses the name outright: a record set
				// that mixes a public and a metadata address is an attack, not
				// a fallback.
				return nil, err
			}
		}
		for _, ip := range ips {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
}

// nodeCheckRedirect keeps the token on the configured host. Go strips only
// Authorization/Cookie on a cross-host redirect, not the custom Token header,
// so following a redirect elsewhere would hand the token to that host. Same
// host (an http -> https upgrade, a trailing-slash fix) is fine; three hops is
// a loop.
func nodeCheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 3 {
		return common.NewError("node redirected too many times")
	}
	if len(via) > 0 && !strings.EqualFold(req.URL.Hostname(), via[0].URL.Hostname()) {
		return common.NewErrorf("node redirected to another host (%s); refusing to send the API token there", req.URL.Hostname())
	}
	if len(via) > 0 && via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
		return common.NewError("node redirected from https to http; refusing to downgrade")
	}
	return nil
}

// validateNodeBaseURL normalises and checks a node address as typed: http or
// https only, a host, an optional port, no credentials, path, query or
// fragment (the web path is its own field). Returns scheme://host[:port].
func validateNodeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(strings.TrimRight(strings.TrimSpace(raw), "/"))
	u, err := url.Parse(raw)
	if err != nil {
		return "", common.NewErrorf("node baseUrl is not a URL: %v", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", common.NewError("node baseUrl must start with http:// or https://")
	}
	if u.User != nil {
		return "", common.NewError("node baseUrl must not carry credentials; use the API token field")
	}
	if u.Hostname() == "" {
		return "", common.NewError("node baseUrl has no host")
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", common.NewError("node baseUrl is scheme://host[:port] only; put the panel path in the web path field")
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return "", common.NewError("node baseUrl port must be 1-65535")
		}
	}
	return scheme + "://" + u.Host, nil
}

// newRequestID returns a random request id for X-Request-Id. Node-side
// idempotency keys on it, so it must not repeat across masters.
func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b[:])
}
