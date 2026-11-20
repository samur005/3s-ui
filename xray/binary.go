package xray

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Lookup order for the Xray binary: an explicit path, then beside the panel
// executable, then PATH. Matches how qWDTT is found.
func FindBinary() (string, error) {
	if p := strings.TrimSpace(os.Getenv("S3UI_XRAY_BIN")); p != "" {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
		return "", fmt.Errorf("S3UI_XRAY_BIN=%s is not a file", p)
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for _, name := range []string{"xray", "Xray"} {
			cand := filepath.Join(dir, name)
			if st, err := os.Stat(cand); err == nil && !st.IsDir() {
				return cand, nil
			}
		}
	}
	if p, err := exec.LookPath("xray"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("xray binary not found: set S3UI_XRAY_BIN, put it next to the panel, or on PATH")
}

// VersionOf runs `xray version` and returns the first line.
func VersionOf(bin string) string {
	out, err := exec.Command(bin, "version").CombinedOutput()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	return line
}
