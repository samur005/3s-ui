// Package fdguard keeps in-process TUN (and other) file descriptors from
// leaking into external children such as qwdtt and xray.
//
// Root cause: sagernet/sing-tun opens /dev/net/tun with O_RDWR only (no
// O_CLOEXEC). Go's os/exec passes stdin/stdout/stderr to the child but does
// not close other non-CLOEXEC descriptors before execve, so those FDs stay
// open in the child. When the panel later closes the core, the sidecar still
// holds the TUN; the next TUNSETIFF returns EBUSY.
//
// Fix: before starting a child, set FD_CLOEXEC on every FD ≥ 3 in this
// process. That does not close anything in the parent — only prevents
// inheritance across exec. Preferred path is Linux close_range(...,
// CLOSE_RANGE_CLOEXEC); older kernels fall back to /proc/self/fd + F_SETFD.
package fdguard
