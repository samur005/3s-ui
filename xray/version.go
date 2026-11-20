package xray

// PinnedRelease is the official Xray-core release this panel builds against
// and ships. Bump it together with scripts/fetch-xray.sh and the release
// workflow. Inspect https://github.com/XTLS/Xray-core/releases before changing.
const PinnedRelease = "v26.11.9"

// TypeVLESS is the inbound type string for an Xray VLESS listener.
const TypeVLESS = "xvless"

// IsType reports whether t is an inbound this package serves.
func IsType(t string) bool { return t == TypeVLESS }

// Types lists them.
func Types() []string { return []string{TypeVLESS} }
