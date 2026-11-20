//go:build !linux

package fdguard

func markCloexecFrom(int) {}
