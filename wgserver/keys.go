package wgserver

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"golang.org/x/crypto/curve25519"
)

// NewKeyPair returns a clamped curve25519 private key and its public key, both
// base64 as WireGuard writes them.
func NewKeyPair() (priv, pub string, err error) {
	var k [32]byte
	if _, err = rand.Read(k[:]); err != nil {
		return "", "", err
	}
	k[0] &= 248
	k[31] = (k[31] & 127) | 64
	p, err := curve25519.X25519(k[:], curve25519.Basepoint)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(k[:]), base64.StdEncoding.EncodeToString(p), nil
}

// NewPresharedKey returns a random 32-byte key, base64.
func NewPresharedKey() (string, error) {
	var k [32]byte
	if _, err := rand.Read(k[:]); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(k[:]), nil
}

// PublicKey derives the public key of a base64 private key.
func PublicKey(priv string) (string, error) {
	raw, err := decodeKey(priv)
	if err != nil {
		return "", err
	}
	p, err := curve25519.X25519(raw, curve25519.Basepoint)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(p), nil
}

func decodeKey(s string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(raw) != 32 {
		return nil, fmt.Errorf("a WireGuard key is 32 bytes of base64")
	}
	return raw, nil
}

// keyHex converts a base64 key to the hex the engine's config interface takes.
func keyHex(s string) (string, error) {
	raw, err := decodeKey(s)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// ValidKey reports whether s is a 32-byte base64 key.
func ValidKey(s string) bool { _, err := decodeKey(s); return err == nil }
