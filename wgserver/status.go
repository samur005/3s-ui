package wgserver

// Status is the live state of one inbound's server.
type Status struct {
	Supported bool   `json:"supported"`
	Running   bool   `json:"running"`
	Iface     string `json:"iface"`
	Routed    bool   `json:"routed"`
	TunReady  bool   `json:"tunReady"`
	Error     string `json:"error,omitempty"`
	// Peers is how many peers the server accepts, Online how many completed a
	// handshake in the last three minutes.
	Peers  int `json:"peers"`
	Online int `json:"online"`
}

// PeerStat is the engine's counters for one peer.
type PeerStat struct {
	PublicKey     string
	Rx, Tx        int64
	LastHandshake int64 // unix seconds, 0 = never
}
