package fdguard

// BeforeChildStart marks open file descriptors so they are not inherited by
// the next os/exec child. Safe to call from multiple goroutines; it never
// closes descriptors. No-op on non-Linux builds.
func BeforeChildStart() {
	markCloexecFrom(3)
}
