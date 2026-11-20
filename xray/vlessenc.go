package xray

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

var (
	reDec = regexp.MustCompile(`(?m)^"decryption":\s*"([^"]+)"`)
	reEnc = regexp.MustCompile(`(?m)^"encryption":\s*"([^"]+)"`)
)

// GenerateVlessEnc runs `xray vlessenc` and returns the Post-Quantum
// (ML-KEM-768) authentication pair. The X25519-only pair is also in the
// command output but we prefer the PQ pair for new inbounds.
func GenerateVlessEnc(bin string) (decryption, encryption string, err error) {
	out, err := exec.Command(bin, "vlessenc").CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("xray vlessenc: %w\n%s", err, out)
	}
	text := string(out)
	// Prefer the second pair (ML-KEM-768) when present.
	decs := reDec.FindAllStringSubmatch(text, -1)
	encs := reEnc.FindAllStringSubmatch(text, -1)
	if len(decs) == 0 || len(encs) == 0 {
		return "", "", fmt.Errorf("xray vlessenc: unexpected output:\n%s", text)
	}
	i := len(decs) - 1
	if i >= len(encs) {
		i = len(encs) - 1
	}
	return decs[i][1], encs[i][1], nil
}

// ParseVlessEncOutput is for tests.
func ParseVlessEncOutput(text string) (decryption, encryption string, err error) {
	decs := reDec.FindAllStringSubmatch(text, -1)
	encs := reEnc.FindAllStringSubmatch(text, -1)
	if len(decs) == 0 || len(encs) == 0 {
		return "", "", fmt.Errorf("no pair in %q", strings.TrimSpace(text))
	}
	i := len(decs) - 1
	if i >= len(encs) {
		i = len(encs) - 1
	}
	return decs[i][1], encs[i][1], nil
}
