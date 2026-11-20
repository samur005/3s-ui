//go:build !linux

package qwdtt

import "time"

// RoutingSupported is false off Linux: the server needs a TUN device and
// policy routing, neither of which the panel drives on this platform.
func RoutingSupported() bool { return false }

func ensureRouting() bool { return false }

func clearRouting() {}

func waitForIface(string, time.Duration) bool { return false }

func linkExists(string) bool { return false }
