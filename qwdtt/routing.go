package qwdtt

import (
	"strconv"
	"strings"
)

const (
	// Interfaces the server creates for client traffic.
	ifaceWG  = "wdtt0"
	ifaceRaw = "wdttraw0"

	// Subnets the server hands client addresses from (and its old ones).
	subnetWG       = "10.68.0.0/16"
	subnetWGLegacy = "10.66.0.0/16"
	subnetRaw      = "10.70.0.0/16"

	// The sing-box TUN inbound the panel adds to the core config.
	TunTag     = "qwdtt-tun"
	TunName    = "qwdtt0"
	TunAddress = "10.253.77.1/30"
	TunMTU     = 1280

	// Policy-routing table that sends qWDTT client traffic into the TUN.
	routeTable = 1977
)

var serverIfaces = []string{ifaceWG, ifaceRaw}

// ruleMissingLookup reports whether `ip rule show` output has no rule that
// ends in "lookup <table>".
func ruleMissingLookup(ruleOutput string, table int) bool {
	needle := "lookup " + strconv.Itoa(table)
	for _, line := range strings.Split(ruleOutput, "\n") {
		if strings.HasSuffix(strings.TrimSpace(line), needle) {
			return false
		}
	}
	return true
}

// linkReady reports whether a network interface exists.
func linkReady(name string) bool { return linkExists(name) }
