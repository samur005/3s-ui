//go:build !linux

package wgserver

// EngineSupported is false off Linux: the server device, policy routing and
// firewall rules are Linux features.
func EngineSupported() bool { return false }

// RoutingSupported is false off Linux.
func RoutingSupported() bool { return false }

// Manager is a stand-in that does nothing.
type Manager struct{}

var defaultMgr = &Manager{}

// Get returns the process-wide manager.
func Get() *Manager { return defaultMgr }

func (m *Manager) Reconcile()            {}
func (m *Manager) Restart(uint)          {}
func (m *Manager) Shutdown()             {}
func (m *Manager) Stats(uint) []PeerStat { return nil }
func (m *Manager) Status(in Inbound) Status {
	return Status{Supported: false, Error: "WireGuard and AmneziaWG servers are only supported on Linux"}
}
