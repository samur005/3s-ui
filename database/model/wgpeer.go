package model

// WgPeer is one client's keys and tunnel address on one WireGuard or
// AmneziaWG inbound: the peer the server accepts and the identity the client
// config is rendered from.
//
// It is a table of its own, not part of the inbound's options, because the
// options travel to the browser and back with every edit of the inbound form,
// and a stale tab saving the form must never rewrite (or leak) client keys.
// Rows are created when a client is bound to the inbound and removed when the
// client or the inbound is deleted; unbinding a client only stops the server
// from accepting the peer, so rebinding gives back the same config.
type WgPeer struct {
	Id        uint `json:"id" gorm:"primaryKey;autoIncrement"`
	InboundId uint `json:"inboundId" gorm:"not null;uniqueIndex:idx_wg_peer,priority:1"`
	ClientId  uint `json:"clientId" gorm:"not null;uniqueIndex:idx_wg_peer,priority:2"`
	// Base64 curve25519 keys; the pre-shared key is optional extra symmetric
	// protection generated per peer.
	PrivateKey   string `json:"-"`
	PublicKey    string `json:"-"`
	PresharedKey string `json:"-"`
	// Address is the peer's IPv4 tunnel address without a prefix length.
	Address string `json:"-"`
}
