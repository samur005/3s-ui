package wgserver

import (
	"fmt"
	"net/netip"
	"strconv"
)

// Names the panel gives the pieces it creates for one inbound. They derive from
// the inbound's id alone, so a restart finds and replaces what the previous
// run left behind.

// IfaceName is the server device ("s3wg3", "s3awg4"); Linux limits names to 15
// bytes, which a 32-bit id fits in.
func IfaceName(typ string, id uint) string {
	if typ == TypeAWG {
		return "s3awg" + strconv.FormatUint(uint64(id), 10)
	}
	return "s3wg" + strconv.FormatUint(uint64(id), 10)
}

// TunTag is the tag of the sing-box TUN inbound that carries the inbound's
// client traffic through the core.
func TunTag(id uint) string { return fmt.Sprintf("wgtun-%d", id) }

// TunName is that TUN's interface name.
func TunName(id uint) string { return "s3c" + strconv.FormatUint(uint64(id), 10) }

// TunAddress gives each inbound its own /30 for the core TUN, from 10.254.0.0/16:
// 4 addresses per inbound, so ids up to 16383 fit.
func TunAddress(id uint) string {
	n := (uint32(id) % 16384) * 4
	return netip.AddrFrom4([4]byte{10, 254, byte(n >> 8), byte(n&0xff) + 1}).String() + "/30"
}

// RouteTable is the policy-routing table that sends the inbound's client
// traffic into its core TUN.
func RouteTable(id uint) int { return 21000 + int(id%20000) }
