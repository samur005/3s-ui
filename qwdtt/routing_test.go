package qwdtt

import (
	"net/netip"
	"strings"
	"testing"
)

func TestServerSubnetsAreDisjointFromTheTun(t *testing.T) {
	tun := netip.MustParsePrefix(TunAddress).Masked()
	var subs []netip.Prefix
	for _, s := range []string{subnetWG, subnetWGLegacy, subnetRaw} {
		p := netip.MustParsePrefix(s)
		subs = append(subs, p)
		if p.Overlaps(tun) {
			t.Errorf("%s overlaps the TUN subnet %s", s, tun)
		}
	}
	for i := range subs {
		for j := i + 1; j < len(subs); j++ {
			if subs[i].Overlaps(subs[j]) {
				t.Errorf("%s overlaps %s", subs[i], subs[j])
			}
		}
	}
}

func TestRuleMissingLookup(t *testing.T) {
	out := "32765:\tfrom all iif wdtt0 lookup 1977\n"
	if ruleMissingLookup(out, routeTable) {
		t.Error("rule present but reported missing")
	}
	if !ruleMissingLookup(out, 1978) {
		t.Error("other table must be missing")
	}
	if !ruleMissingLookup("", routeTable) {
		t.Error("empty output must be missing")
	}
	// Must not match a longer table number sharing the prefix.
	if !ruleMissingLookup("32765:\tfrom all iif wdtt0 lookup 19770\n", routeTable) {
		t.Error("lookup 19770 is not lookup 1977")
	}
}

func TestTunNameFitsLinuxIfnameLimit(t *testing.T) {
	for _, n := range []string{TunName, ifaceWG, ifaceRaw} {
		if len(n) > 15 || strings.ContainsAny(n, " /") {
			t.Errorf("bad interface name %q", n)
		}
	}
}

func TestBinaryPathHonoursEnv(t *testing.T) {
	t.Setenv(EnvBin, "/opt/qwdtt-custom")
	if got := BinaryPath(); got != "/opt/qwdtt-custom" {
		t.Errorf("BinaryPath = %q", got)
	}
}
