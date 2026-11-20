//go:build linux

package fdguard

import (
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

func markCloexecFrom(first int) {
	if first < 0 {
		first = 0
	}
	// Atomic mark on kernels that support close_range + CLOSE_RANGE_CLOEXEC
	// (Linux ≥ 5.11). Does not close FDs — only sets FD_CLOEXEC.
	err := unix.CloseRange(uint(first), ^uint(0), unix.CLOSE_RANGE_CLOEXEC)
	if err == nil {
		return
	}
	// ENOSYS / EINVAL → walk /proc/self/fd and F_SETFD.
	markCloexecProc(first)
}

func markCloexecProc(first int) {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return
	}
	for _, e := range entries {
		fd, err := strconv.Atoi(e.Name())
		if err != nil || fd < first {
			continue
		}
		flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
		if err != nil {
			continue
		}
		if flags&unix.FD_CLOEXEC != 0 {
			continue
		}
		_, _ = unix.FcntlInt(uintptr(fd), unix.F_SETFD, flags|unix.FD_CLOEXEC)
	}
}
