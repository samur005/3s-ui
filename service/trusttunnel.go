package service

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
	"github.com/samur005/3s-ui/logger"
	"github.com/samur005/3s-ui/util/common"
	"gorm.io/gorm"
)

const ttVersion = "v1.3.1"

var (
	ttMu     sync.Mutex
	ttCmd    *exec.Cmd
	ttBinDir = filepath.Join(os.TempDir(), "3s-ui-trusttunnel")
)

func init() {
	registerExtDriver(&extDriver{
		Type:   "trusttunnel",
		Engine: EngineExternal,
		Normalize: func(tx *gorm.DB, id uint, options, stored json.RawMessage) (json.RawMessage, error) {
			var opt struct {
				ListenPort  int    `json:"listen_port"`
				Credentials string `json:"credentials"`
			}
			if err := json.Unmarshal(options, &opt); err != nil {
				return nil, err
			}
			if opt.ListenPort <= 0 || opt.ListenPort > 65535 {
				return nil, common.NewError("invalid listen_port")
			}
			return json.Marshal(opt)
		},
		Apply: applyTrustTunnel,
	})
}

func applyTrustTunnel() {
	ttMu.Lock()
	defer ttMu.Unlock()

	// Stop existing
	if ttCmd != nil && ttCmd.Process != nil {
		_ = ttCmd.Process.Kill()
		ttCmd = nil
	}

	db := database.GetDB()
	var ib model.Inbound
	if err := db.Where("type = ? AND enable = ?", "trusttunnel", true).First(&ib).Error; err != nil {
		return // nothing enabled
	}

	var opt struct {
		ListenPort  int    `json:"listen_port"`
		Credentials string `json:"credentials"`
	}
	if err := json.Unmarshal(ib.Options, &opt); err != nil {
		logger.Warning("trusttunnel options: ", err)
		return
	}

	bin, err := ensureTrustTunnelBinary()
	if err != nil {
		logger.Warning("trusttunnel binary: ", err)
		return
	}

	cfgDir := filepath.Join(ttBinDir, "cfg")
	_ = os.MkdirAll(cfgDir, 0755)

	// Minimal vpn.toml
	vpnToml := fmt.Sprintf(`[endpoint]
listen_address = "0.0.0.0:%d"
credentials_file = "%s/credentials.toml"
`, opt.ListenPort, cfgDir)
	_ = os.WriteFile(filepath.Join(cfgDir, "vpn.toml"), []byte(vpnToml), 0644)

	// credentials.toml
	creds := "[users]\n"
	for _, line := range splitLines(opt.Credentials) {
		if line != "" {
			creds += fmt.Sprintf("%s = \"\"\n", line) // simple user:pass as key with empty value for now
		}
	}
	_ = os.WriteFile(filepath.Join(cfgDir, "credentials.toml"), []byte(creds), 0644)

	// hosts.toml stub
	hosts := `[hosts.default]
type = "self_signed"
`
	_ = os.WriteFile(filepath.Join(cfgDir, "hosts.toml"), []byte(hosts), 0644)

	cmd := exec.Command(bin, filepath.Join(cfgDir, "vpn.toml"), filepath.Join(cfgDir, "hosts.toml"))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		logger.Warning("trusttunnel start: ", err)
		return
	}
	ttCmd = cmd
	logger.Info("trusttunnel started pid=", cmd.Process.Pid)
}

func ensureTrustTunnelBinary() (string, error) {
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x86_64"
	} else if arch == "arm64" {
		arch = "aarch64"
	} else {
		return "", fmt.Errorf("unsupported arch %s", arch)
	}
	name := fmt.Sprintf("trusttunnel-%s-linux-%s.tar.gz", ttVersion, arch)
	url := fmt.Sprintf("https://github.com/TrustTunnel/TrustTunnel/releases/download/%s/%s", ttVersion, name)
	dest := filepath.Join(ttBinDir, "trusttunnel_endpoint")
	if _, err := os.Stat(dest); err == nil {
		return dest, nil
	}
	_ = os.MkdirAll(ttBinDir, 0755)
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download %s: %s", url, resp.Status)
	}
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return "", err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if h.Typeflag == tar.TypeReg && filepath.Base(h.Name) == "trusttunnel_endpoint" {
			f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
			if err != nil {
				return "", err
			}
			_, err = io.Copy(f, tr)
			f.Close()
			if err != nil {
				return "", err
			}
			return dest, nil
		}
	}
	return "", fmt.Errorf("binary not found in archive")
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
