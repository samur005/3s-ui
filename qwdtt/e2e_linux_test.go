//go:build linux && qwdtt_e2e

package qwdtt

// An end-to-end run against a real qWDTT server binary: the panel writes the
// per-client passwords, starts the server, a real qWDTT client connects with
// a client's own password and moves traffic, the traffic is credited back to
// the client, and disabling the client takes the password away.
//
// Not part of `go test ./...`. It needs root (the server makes a TUN device),
// and stand-ins for VK: a local TURN relay and a client build whose
// credential fetch returns that relay (VK's TURN itself cannot be reached
// from a test). scripts/qwdtt-e2e.sh builds both and runs this.
//
//	QWDTT_E2E_BIN     the qwdtt server binary (scripts/build-qwdtt.sh)
//	QWDTT_E2E_CLIENT  a qWDTT go_client built with E2E_TURN_URL support
//	QWDTT_E2E_TURN    TURN host:port handed to it, e.g. 127.0.0.1:3478
//	QWDTT_E2E_HOSTIP  an address of this host the tunnel can reach (no NAT needed)

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
	"golang.org/x/net/proxy"
)

func e2eEnv(t *testing.T, name string) string {
	v := os.Getenv(name)
	if v == "" {
		t.Skip(name + " not set")
	}
	return v
}

// startClient runs the qWDTT client in SOCKS mode on socksPort.
func startClient(t *testing.T, bin, turnURL, peer, hash, password, device string, socksPort int) {
	t.Helper()
	cmd := exec.Command(bin, "-mode", "socks", "-socks", fmt.Sprintf("127.0.0.1:%d", socksPort),
		"-listen", fmt.Sprintf("127.0.0.1:%d", socksPort+1000),
		"-peer", peer, "-vk", hash, "-password", password, "-device-id", device,
		"-n", "1", "-diag-auto=false")
	cmd.Env = append(os.Environ(), "E2E_TURN_URL="+turnURL)
	logf, _ := os.Create(filepath.Join(t.TempDir(), device+".log"))
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		_ = logf.Close()
		if t.Failed() {
			b, _ := os.ReadFile(logf.Name())
			t.Logf("--- client %s log ---\n%s", device, tail(string(b), 3000))
		}
	})
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

// fetchVia GETs target through a SOCKS5 proxy, retrying until the tunnel is up.
func fetchVia(socksPort int, target string, wait time.Duration) (int, error) {
	d, _ := proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", socksPort), nil, proxy.Direct)
	cd := d.(proxy.ContextDialer)
	hc := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DialContext: cd.DialContext}}
	deadline := time.Now().Add(wait)
	var last error
	for time.Now().Before(deadline) {
		resp, err := hc.Get(target)
		if err == nil {
			n, _ := io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			return int(n), nil
		}
		last = err
		time.Sleep(time.Second)
	}
	return 0, last
}

func TestE2EPerClientPasswords(t *testing.T) {
	bin := e2eEnv(t, "QWDTT_E2E_BIN")
	client := e2eEnv(t, "QWDTT_E2E_CLIENT")
	turnURL := e2eEnv(t, "QWDTT_E2E_TURN")
	hostIP := e2eEnv(t, "QWDTT_E2E_HOSTIP")
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	t.Setenv(EnvBin, bin)
	initDB(t)
	db := database.GetDB()

	// Something to fetch through the tunnel: 2 MiB from this host.
	payload := strings.Repeat("x", 2<<20)
	ln, err := net.Listen("tcp", hostIP+":0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, payload) })}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Shutdown(context.Background()) })
	target := "http://" + ln.Addr().String() + "/"

	dir := t.TempDir()
	cfg := Default()
	cfg.Enabled = true
	cfg.ListenAddr = "127.0.0.1:56100"
	cfg.WGPort = 56101
	cfg.ListenRaw = ""
	cfg.ConfigDir = dir
	cfg.VkHashes = "e2ehash"
	cfg.AutoVkHash = false
	cfg.RouteThroughCore = false
	cfg.MaxDevices = 2
	if _, err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	in, _ := LoadInbound(db)
	ids, _ := json.Marshal([]uint{in.Id})
	for _, n := range []string{"alice", "bob"} {
		c := model.Client{Name: n, Enable: true, Inbounds: ids, Config: json.RawMessage(`{}`)}
		if err := db.Create(&c).Error; err != nil {
			t.Fatal(err)
		}
	}

	Get().Reconcile()
	t.Cleanup(func() {
		Get().Shutdown()
		if t.Failed() {
			t.Logf("--- server log ---\n%s", strings.Join(Get().Logs(200), "\n"))
		}
	})
	if !Get().proc.running() {
		t.Fatalf("server not running: %s", strings.Join(Get().Logs(50), "\n"))
	}
	time.Sleep(2 * time.Second)

	var alice, bob model.Client
	db.Where("name = ?", "alice").First(&alice)
	db.Where("name = ?", "bob").First(&bob)
	alicePw, bobPw := ClientPassword(alice.Config), ClientPassword(bob.Config)
	if alicePw == "" || bobPw == "" || alicePw == bobPw {
		t.Fatalf("passwords %q %q", alicePw, bobPw)
	}
	users, _ := ListServerUsers(dir)
	t.Logf("server users after start: %+v", users)
	if len(users) != 2 {
		t.Fatalf("users = %+v", users)
	}

	// 1. alice connects with her own password and fetches.
	startClient(t, client, turnURL, cfg.ListenAddr, "e2ehash", alicePw, "devAlice", 18081)
	n, err := fetchVia(18081, target, 60*time.Second)
	if err != nil || n != len(payload) {
		t.Fatalf("alice through the tunnel: %d bytes, %v", n, err)
	}
	t.Logf("alice fetched %d bytes through qWDTT", n)

	// 2. A wrong password gets nowhere.
	startClient(t, client, turnURL, cfg.ListenAddr, "e2ehash", "not-a-password", "devEve", 18082)
	if _, err := fetchVia(18082, target, 20*time.Second); err == nil {
		t.Fatal("a wrong password got through")
	}
	t.Log("wrong password refused")

	// 3. Traffic: the server saves its counters once a minute.
	t.Log("waiting for the server's counter save (~60s)...")
	deadline := time.Now().Add(80 * time.Second)
	for time.Now().Before(deadline) {
		Get().Reconcile()
		db.Where("name = ?", "alice").First(&alice)
		if alice.Down > int64(len(payload)) {
			break
		}
		time.Sleep(5 * time.Second)
	}
	t.Logf("alice credited up=%d down=%d", alice.Up, alice.Down)
	if alice.Down < int64(len(payload)) {
		t.Fatalf("alice's traffic not credited: up=%d down=%d", alice.Up, alice.Down)
	}
	var inb model.Inbound
	db.Where("id = ?", in.Id).First(&inb)
	if inb.Down < int64(len(payload)) {
		t.Errorf("inbound traffic not credited: %d/%d", inb.Up, inb.Down)
	}

	// 4. alice disabled: her password and device leave the server on SIGHUP,
	// and the tunnel stops carrying traffic.
	db.Model(model.Client{}).Where("name = ?", "alice").Update("enable", false)
	Get().Reconcile()
	users, _ = ListServerUsers(dir)
	for _, u := range users {
		if u.Client == "alice" {
			t.Fatalf("alice still on the server: %+v", users)
		}
	}
	time.Sleep(3 * time.Second)
	if _, err := fetchVia(18081, target, 15*time.Second); err == nil {
		t.Error("alice's tunnel still works after she was disabled")
	} else {
		t.Logf("alice after disable: %v", err)
	}

	// 5. bob still connects.
	startClient(t, client, turnURL, cfg.ListenAddr, "e2ehash", bobPw, "devBob", 18083)
	if n, err := fetchVia(18083, target, 60*time.Second); err != nil || n != len(payload) {
		t.Fatalf("bob: %d, %v", n, err)
	}
	t.Log("bob fetched through qWDTT after alice was removed")
}
