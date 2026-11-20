//go:build linux

package fdguard

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// openTunNoCloexec mirrors sing-tun's open(): O_RDWR without O_CLOEXEC.
func openTunNoCloexec(t *testing.T, name string) int {
	t.Helper()
	fd, err := unix.Open("/dev/net/tun", unix.O_RDWR, 0)
	if err != nil {
		t.Skipf("open /dev/net/tun: %v (need CAP_NET_ADMIN)", err)
	}
	ifr, err := unix.NewIfreq(name)
	if err != nil {
		unix.Close(fd)
		t.Fatal(err)
	}
	ifr.SetUint16(unix.IFF_TUN | unix.IFF_NO_PI)
	if err := unix.IoctlIfreq(fd, unix.TUNSETIFF, ifr); err != nil {
		unix.Close(fd)
		t.Skipf("TUNSETIFF %s: %v", name, err)
	}
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	if err != nil {
		unix.Close(fd)
		t.Fatal(err)
	}
	if flags&unix.FD_CLOEXEC != 0 {
		unix.Close(fd)
		t.Fatal("expected TUN fd without CLOEXEC (sing-tun repro)")
	}
	return fd
}

func reopenTun(t *testing.T, name string) error {
	t.Helper()
	fd, err := unix.Open("/dev/net/tun", unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	ifr, err := unix.NewIfreq(name)
	if err != nil {
		return err
	}
	ifr.SetUint16(unix.IFF_TUN | unix.IFF_NO_PI)
	return unix.IoctlIfreq(fd, unix.TUNSETIFF, ifr)
}

func TestMarkSetsCloexecBit(t *testing.T) {
	name := fmt.Sprintf("s3m%d", os.Getpid()%100000)
	fd := openTunNoCloexec(t, name)
	defer unix.Close(fd)
	BeforeChildStart()
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	if err != nil {
		t.Fatal(err)
	}
	if flags&unix.FD_CLOEXEC == 0 {
		t.Fatal("BeforeChildStart did not set FD_CLOEXEC on TUN fd")
	}
}

func TestBeforeChildStart_AllowsCoreRestart(t *testing.T) {
	name := fmt.Sprintf("s3r%d", os.Getpid()%100000)
	fd := openTunNoCloexec(t, name)
	cmd := exec.Command("sleep", "60")
	BeforeChildStart()
	if err := cmd.Start(); err != nil {
		unix.Close(fd)
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	if err := unix.Close(fd); err != nil {
		t.Fatal(err)
	}
	if err := reopenTun(t, name); err != nil {
		t.Fatalf("re-TUNSETIFF after protected spawn: %v (child likely inherited TUN)", err)
	}
}

func TestUnprotectedChildPinsTun(t *testing.T) {
	name := fmt.Sprintf("s3u%d", os.Getpid()%100000)
	fd := openTunNoCloexec(t, name)
	cmd := exec.Command("sleep", "60")
	// Deliberately no BeforeChildStart — documents the live bug.
	if err := cmd.Start(); err != nil {
		unix.Close(fd)
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	if err := unix.Close(fd); err != nil {
		t.Fatal(err)
	}
	err := reopenTun(t, name)
	if err == nil {
		t.Skip("unprotected child did not pin TUN (unexpected on this kernel/Go); inheritance path not observed")
	}
	t.Logf("unprotected spawn pins TUN as on VPS: %v", err)
}

func TestChildDoesNotSeeTunFd(t *testing.T) {
	name := fmt.Sprintf("s3c%d", os.Getpid()%100000)
	fd := openTunNoCloexec(t, name)
	defer unix.Close(fd)
	script := fmt.Sprintf("test ! -e /proc/self/fd/%d", fd)
	BeforeChildStart()
	if err := exec.Command("sh", "-c", script).Run(); err != nil {
		t.Fatalf("protected child still has parent TUN fd %d open: %v", fd, err)
	}
}

func TestBeforeChildStart_BlocksPlainFdInheritance(t *testing.T) {
	// Open a regular file without CLOEXEC (like a leaky device fd).
	f, err := os.OpenFile(filepath.Join(t.TempDir(), "x"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fd := int(f.Fd())
	// Clear CLOEXEC if Go set it (os.OpenFile uses CLOEXEC); re-open via unix.
	_ = f.Close()
	fd, err = unix.Open(filepath.Join(t.TempDir(), "y"), unix.O_CREAT|unix.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	flags, _ := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	if flags&unix.FD_CLOEXEC != 0 {
		t.Fatal("fixture fd unexpectedly has CLOEXEC")
	}
	BeforeChildStart()
	flags, err = unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	if err != nil {
		t.Fatal(err)
	}
	if flags&unix.FD_CLOEXEC == 0 {
		t.Fatal("BeforeChildStart did not set CLOEXEC on plain fd")
	}
	cmd := exec.Command("sh", "-c", fmt.Sprintf("test ! -e /proc/self/fd/%d", fd))
	if err := cmd.Run(); err != nil {
		t.Fatalf("child still sees fd %d after protect: %v", fd, err)
	}
}

func TestUnprotectedChildInheritsPlainFd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "z")
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	cmd := exec.Command("sh", "-c", fmt.Sprintf("test -e /proc/self/fd/%d", fd))
	if err := cmd.Run(); err != nil {
		t.Fatalf("expected unprotected child to inherit fd %d: %v", fd, err)
	}
}
