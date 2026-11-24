// Package secretbox seals small secrets stored in the panel database (node API
// tokens, mTLS client keys, persisted idempotent responses) with AES-256-GCM.
//
// The key lives next to the database in its own file (secret.key, mode 0600),
// not in the database: a copied .db file or a panel backup alone does not
// reveal the tokens. Sealed values carry a "enc:v1:" prefix; anything else is
// read as legacy plaintext, so an upgraded panel reads old rows and the
// migration (database.migrateSealSecrets) rewrites them sealed.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const Prefix = "enc:v1:"

var (
	mu   sync.RWMutex
	aead cipher.AEAD
)

// ErrNoKey is returned by Open for a sealed value when no key is loaded.
var ErrNoKey = errors.New("secretbox: key not loaded")

// LoadOrCreate loads the key at path, creating a fresh random one when the
// file does not exist. SUI_SECRET_KEY (base64, 32 bytes) overrides the file.
func LoadOrCreate(path string) error {
	var key []byte
	if env := strings.TrimSpace(os.Getenv("SUI_SECRET_KEY")); env != "" {
		k, err := base64.StdEncoding.DecodeString(env)
		if err != nil || len(k) != 32 {
			return errors.New("secretbox: SUI_SECRET_KEY must be 32 bytes, base64")
		}
		key = k
	} else {
		raw, err := os.ReadFile(path)
		switch {
		case err == nil:
			k, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
			if derr != nil || len(k) != 32 {
				return errors.New("secretbox: " + path + " is not a valid key file")
			}
			key = k
		case errors.Is(err, os.ErrNotExist):
			key = make([]byte, 32)
			if _, err := rand.Read(key); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(key)+"\n"), 0o600); err != nil {
				return err
			}
		default:
			return err
		}
	}
	return SetKey(key)
}

// SetKey installs a raw 32-byte key (tests, LoadOrCreate).
func SetKey(key []byte) error {
	if len(key) != 32 {
		return errors.New("secretbox: key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	mu.Lock()
	aead = g
	mu.Unlock()
	return nil
}

// Reset drops the key (tests).
func Reset() {
	mu.Lock()
	aead = nil
	mu.Unlock()
}

// Ready reports whether a key is loaded.
func Ready() bool {
	mu.RLock()
	defer mu.RUnlock()
	return aead != nil
}

func IsSealed(v string) bool { return strings.HasPrefix(v, Prefix) }

// Seal encrypts v. Empty and already sealed values are returned unchanged,
// and so is everything when no key is loaded (Ready reports that).
func Seal(v string) (string, error) {
	if v == "" || IsSealed(v) {
		return v, nil
	}
	mu.RLock()
	g := aead
	mu.RUnlock()
	if g == nil {
		return v, nil
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := g.Seal(nonce, nonce, []byte(v), []byte("3s-ui"))
	return Prefix + base64.RawStdEncoding.EncodeToString(out), nil
}

// Open decrypts a sealed value; plaintext (legacy rows) is returned as is.
func Open(v string) (string, error) {
	if !IsSealed(v) {
		return v, nil
	}
	mu.RLock()
	g := aead
	mu.RUnlock()
	if g == nil {
		return "", ErrNoKey
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(v, Prefix))
	if err != nil || len(raw) < g.NonceSize() {
		return "", errors.New("secretbox: malformed sealed value")
	}
	plain, err := g.Open(nil, raw[:g.NonceSize()], raw[g.NonceSize():], []byte("3s-ui"))
	if err != nil {
		return "", errors.New("secretbox: cannot decrypt (different key?)")
	}
	return string(plain), nil
}
