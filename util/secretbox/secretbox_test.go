package secretbox

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	t.Cleanup(Reset)
	path := filepath.Join(t.TempDir(), "secret.key")
	if err := LoadOrCreate(path); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file %v %v", fi, err)
	}
	sealed, err := Seal("hunter2")
	if err != nil || !IsSealed(sealed) || strings.Contains(sealed, "hunter2") {
		t.Fatalf("seal = %q %v", sealed, err)
	}
	if again, _ := Seal(sealed); again != sealed {
		t.Fatal("sealing twice must be a no-op")
	}
	if v, err := Open(sealed); err != nil || v != "hunter2" {
		t.Fatalf("open = %q %v", v, err)
	}
	if v, err := Open("legacy-plain"); err != nil || v != "legacy-plain" {
		t.Fatalf("plaintext must pass through: %q %v", v, err)
	}
	// Same file, new process: the key is reused.
	Reset()
	if err := LoadOrCreate(path); err != nil {
		t.Fatal(err)
	}
	if v, err := Open(sealed); err != nil || v != "hunter2" {
		t.Fatalf("reopen with the same key = %q %v", v, err)
	}
	// Another key cannot open it.
	Reset()
	if err := SetKey([]byte(strings.Repeat("k", 32))); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(sealed); err == nil {
		t.Fatal("opened with the wrong key")
	}
	// No key at all.
	Reset()
	if _, err := Open(sealed); err != ErrNoKey {
		t.Fatalf("no key: %v", err)
	}
	if v, _ := Seal("x"); v != "x" {
		t.Fatal("Seal without a key must leave the value as is")
	}
}

func TestKeyFromEnvironment(t *testing.T) {
	t.Cleanup(Reset)
	t.Setenv("SUI_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("e", 32))))
	path := filepath.Join(t.TempDir(), "secret.key")
	if err := LoadOrCreate(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("a key file was written although SUI_SECRET_KEY is set")
	}
	if err := SetKey([]byte("short")); err == nil {
		t.Fatal("short key accepted")
	}
}
