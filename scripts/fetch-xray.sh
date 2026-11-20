#!/usr/bin/env bash
# Downloads the pinned official Xray-core release for the host (or GOOS/GOARCH)
# into the given output path. Used by the release workflow, Docker and local
# smoke builds. Bump XRAY_REF together with xray/version.go.
#
# Verifies the zip against the .dgst file published next to the asset
# (SHA256 line). Asset names match
# https://github.com/XTLS/Xray-core/releases/tag/$XRAY_REF
set -euo pipefail
XRAY_REF="${XRAY_REF:-v26.11.9}"
out="${1:-xray}"
os="${GOOS:-$(uname -s | tr '[:upper:]' '[:lower:]')}"
arch="${GOARCH:-$(uname -m)}"
goarm="${GOARM:-}"

case "$arch" in
  x86_64|amd64) xarch=64 ;;
  aarch64|arm64) xarch=arm64-v8a ;;
  arm|armv7*|armv6*|armv5*)
    # Release matrix sets GOARM=5/6/7; uname alone is not enough.
    case "$goarm" in
      5) xarch=arm32-v5 ;;
      6) xarch=arm32-v6 ;;
      7|"") xarch=arm32-v7a ;;
      *) echo "unsupported GOARM=$goarm for arm" >&2; exit 1 ;;
    esac
    ;;
  i386|i686|386) xarch=32 ;;
  s390x) xarch=s390x ;;
  loong64) xarch=loong64 ;;
  riscv64) xarch=riscv64 ;;
  ppc64le) xarch=ppc64le ;;
  *) echo "unsupported arch: $arch" >&2; exit 1 ;;
esac

case "$os" in
  linux) asset="Xray-linux-${xarch}.zip" ;;
  darwin)
    case "$xarch" in
      64) asset="Xray-macos-64.zip" ;;
      arm64-v8a) asset="Xray-macos-arm64-v8a.zip" ;;
      *) echo "unsupported darwin arch: $xarch" >&2; exit 1 ;;
    esac
    ;;
  windows)
    case "$xarch" in
      64) asset="Xray-windows-64.zip" ;;
      32) asset="Xray-windows-32.zip" ;;
      arm64-v8a) asset="Xray-windows-arm64-v8a.zip" ;;
      *) echo "unsupported windows arch: $xarch" >&2; exit 1 ;;
    esac
    ;;
  *) echo "unsupported os: $os" >&2; exit 1 ;;
esac

base="https://github.com/XTLS/Xray-core/releases/download/${XRAY_REF}"
url="${base}/${asset}"
dgst_url="${base}/${asset}.dgst"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
echo "fetching $url"
curl -fsSL -o "$work/x.zip" "$url"
echo "fetching $dgst_url"
curl -fsSL -o "$work/x.zip.dgst" "$dgst_url"

# Official .dgst is several hash lines; require the SHA256 one.
# Official .dgst looks like: SHA2-256= <hex>
want=$(awk -F'= *' '/^SHA2-256/{gsub(/\r/,"",$2); print $2; exit}' "$work/x.zip.dgst")
if [[ -z "$want" ]]; then
  want=$(grep -Eo '[A-Fa-f0-9]{64}' "$work/x.zip.dgst" | head -1 | tr -d '\r')
fi
got=$(sha256sum "$work/x.zip" | awk '{print $1}')
if [[ -z "$want" ]]; then
  echo "could not parse SHA256 from $asset.dgst:" >&2
  cat "$work/x.zip.dgst" >&2
  exit 1
fi
if [[ "${want,,}" != "${got,,}" ]]; then
  echo "SHA256 mismatch for $asset: want $want got $got" >&2
  exit 1
fi
echo "SHA256 ok ($got)"

unzip -qo "$work/x.zip" -d "$work"
src="$work/xray"
[[ -f "$work/xray.exe" ]] && src="$work/xray.exe"
if [[ ! -f "$src" ]]; then
  echo "zip did not contain xray binary:" >&2
  ls -la "$work" >&2
  exit 1
fi
install -m 755 "$src" "$out"
echo "installed $out ($XRAY_REF $asset)"
