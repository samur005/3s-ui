package qwdtt

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/samur005/3s-ui/logger"
)

func linkExists(name string) bool {
	return exec.CommandContext(context.Background(), "ip", "link", "show", name).Run() == nil
}

func runQuiet(name string, args ...string) {
	if out, err := exec.CommandContext(context.Background(), name, args...).CombinedOutput(); err != nil {
		logger.Warningf("qwdtt: %s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
}

// RoutingSupported is true on Linux, where policy routing is available.
func RoutingSupported() bool { return true }

// ensureRouting makes traffic that arrives on the server's interfaces leave
// through the sing-box TUN: a default route in a private table and an
// "iif <iface> lookup <table>" rule per interface. It is idempotent and
// reports whether the routing is complete (the TUN exists and at least one
// server interface exists).
func ensureRouting() bool {
	if !linkExists(TunName) {
		return false
	}
	table := strconv.Itoa(routeTable)
	any := false
	for _, iface := range serverIfaces {
		if linkExists(iface) {
			any = true
		}
	}
	if !any {
		return false
	}
	runQuiet("ip", "route", "replace", "default", "dev", TunName, "table", table)
	// Loose reverse-path filtering: replies come back on the TUN, not on the
	// interface the request arrived on.
	runQuiet("sysctl", "-qw", "net.ipv4.conf."+TunName+".rp_filter=2")
	runQuiet("sysctl", "-qw", "net.ipv4.ip_forward=1")
	for _, iface := range serverIfaces {
		if !linkExists(iface) {
			continue
		}
		out, err := exec.CommandContext(context.Background(), "ip", "rule", "show", "iif", iface).Output()
		if err != nil || ruleMissingLookup(string(out), routeTable) {
			runQuiet("ip", "rule", "add", "iif", iface, "lookup", table)
		}
	}
	// The server NATs its client subnets itself. With routing through the core
	// that masquerade would hide the client address from the core's rules, and
	// the core does its own NAT on the way out.
	for _, subnet := range []string{subnetWG, subnetWGLegacy, subnetRaw} {
		stripMasquerade(subnet)
	}
	return true
}

func stripMasquerade(subnet string) {
	for i := 0; i < 4; i++ {
		if exec.CommandContext(context.Background(), "iptables", "-t", "nat", "-D", "POSTROUTING",
			"-s", subnet, "-j", "MASQUERADE").Run() != nil {
			return
		}
	}
}

// clearRouting removes the rules and the table ensureRouting installed.
func clearRouting() {
	table := strconv.Itoa(routeTable)
	for _, iface := range serverIfaces {
		for i := 0; i < 4; i++ {
			if exec.CommandContext(context.Background(), "ip", "rule", "del", "iif", iface, "lookup", table).Run() != nil {
				break
			}
		}
	}
	_ = exec.CommandContext(context.Background(), "ip", "route", "flush", "table", table).Run()
}

// waitForIface waits for a link to appear, up to d.
func waitForIface(name string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for !linkExists(name) {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(200 * time.Millisecond)
	}
	return true
}
