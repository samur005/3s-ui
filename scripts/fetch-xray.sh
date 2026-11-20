#!/usr/bin/env bash
# Downloads the pinned official Xray-core release for the host (or GOOS/GOARCH)
# into the given output path. Used by the release workflow, Docker and local
# smoke builds. Bump XRAY_REF together with xray/version.go.
set -euo pipefail
XRAY_REF="${XRAY_REF:-v26.11.9}"
out="${1:-xray}"
os="${GOOS:-$(uname -s | tr '[:upper:]' '[:lower:]')}"
arch="${GOARCH:-$(uname -m)}"
case "$arch" in
  x86_64|amd64) arch=64 ;;
  aarch64|arm64) arch=arm64-v8a ;;
  armv7*|arm) arch=arm32-v7a ;;
  armv6*) arch=arm32-v6 ;;
  i386|i686|386) arch=32 ;;
  *) echo "unsupported arch: $arch" >&2; exit 1 ;;
esac
case "$os" in
  linux) asset="Xray-linux-${arch}.zip" ;;
  darwin) asset="Xray-macos-${arch}.zip" ;;
  windows) asset="Xray-windows-${arch}.zip" ;;
  *) echo "unsupported os: $os" >&2; exit 1 ;;
esac
url="https://github.com/XTLS/Xray-core/releases/download/${XRAY_REF}/${asset}"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
echo "fetching $url"
curl -fsSL -o "$work/x.zip" "$url"
unzip -qo "$work/x.zip" -d "$work"
src="$work/xray"
[[ -f "$work/xray.exe" ]] && src="$work/xray.exe"
install -m 755 "$src" "$out"
echo "installed $out ($XRAY_REF)"
