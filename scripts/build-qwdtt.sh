#!/usr/bin/env bash
# Builds the qWDTT server sidecar that 3S-UI supervises (see qwdtt/ and the
# qWDTT section of the README).
#
# The binary is the `./server` command of SpaceNeuroX/proxy-turn-vk-android,
# pinned to a commit, plus scripts/patches/qwdtt-subnet.patch (moves the
# tunnel subnet 10.66.0.0/16 -> 10.68.0.0/16 so it cannot clash with a wdtt
# server on the same host). Pure Go, so it cross-compiles with no toolchain.
#
# Usage:
#   scripts/build-qwdtt.sh [output-path]            # host platform
#   GOOS=linux GOARCH=arm64 scripts/build-qwdtt.sh qwdtt
#   GOARCH=arm GOARM=7 scripts/build-qwdtt.sh qwdtt
#
# To update qWDTT: bump QWDTT_REF below (a tag or a full commit sha), run this
# script, and if the patch no longer applies, re-create it against the new
# upstream tree. CI (release.yml) and the Dockerfiles call this same script,
# so that is the only place the version lives.
set -euo pipefail

QWDTT_REPO="${QWDTT_REPO:-https://github.com/SpaceNeuroX/proxy-turn-vk-android.git}"
# v1.4.4
QWDTT_REF="${QWDTT_REF:-a296c57eaba69bb9479a24f9157856490890e47d}"

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
out="${1:-qwdtt}"
case "$out" in /*) ;; *) out="$PWD/$out" ;; esac

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

git init -q "$work/src"
git -C "$work/src" remote add origin "$QWDTT_REPO"
git -C "$work/src" fetch -q --depth=1 origin "$QWDTT_REF"
git -C "$work/src" checkout -q FETCH_HEAD
git -C "$work/src" apply --whitespace=nowarn "$here/patches/qwdtt-subnet.patch"

(
	cd "$work/src"
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$out" ./server
)
chmod 755 "$out"
echo "built $out ($(git -C "$work/src" rev-parse --short HEAD) + qwdtt-subnet.patch)"
