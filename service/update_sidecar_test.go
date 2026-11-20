package service

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSwapSidecar(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "stage-qwdtt")
	dst := filepath.Join(dir, "qwdtt")

	// no release asset: no-op, no error
	if err := swapSidecar(src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatal("dst must not be created without a source")
	}

	// first install, then replace an existing (possibly busy) file
	for _, content := range []string{"v1", "v2"} {
		if err := os.WriteFile(src, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := swapSidecar(src, dst); err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(dst)
		if string(got) != content {
			t.Fatalf("got %q want %q", got, content)
		}
		if st, _ := os.Stat(dst); st.Mode().Perm()&0o111 == 0 {
			t.Fatal("sidecar must be executable")
		}
		if _, err := os.Stat(dst + ".new"); !os.IsNotExist(err) {
			t.Fatal("staging file left behind")
		}
	}
}
