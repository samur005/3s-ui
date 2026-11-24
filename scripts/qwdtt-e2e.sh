#!/usr/bin/env bash
# End-to-end test of 3S-UI's qWDTT per-client users against the real qWDTT
# server (qwdtt/e2e_linux_test.go). VK's TURN relays cannot be used from a
# test, so two stand-ins are built in a temp dir, never shipped:
#   - a local TURN relay (pion/turn, scripts/qwdtt-e2e/turnsrv.go.txt);
#   - the qWDTT go_client (SpaceNeuroX/proxy-turn-vk-android, GPL-3.0) with
#     its VK credential fetch short-circuited to that relay when E2E_TURN_URL
#     is set. Everything else -- DTLS, the password handshake, WireGuard, the
#     server's passwords.json, SIGHUP reload, traffic counters -- is real.
# Needs Go, git, python3 and sudo (the server creates a TUN device and nft NAT
# rules; the script removes the `wdtt` nft tables afterwards).
#
#   scripts/qwdtt-e2e.sh [server-binary]
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(dirname "$here")"
CLIENT_REF="${CLIENT_REF:-v1.4.5}"
work="$(mktemp -d)"
cleanup() {
	[[ -n "${turn_pid:-}" ]] && kill "$turn_pid" 2>/dev/null || true
	sudo nft delete table ip wdtt 2>/dev/null || true
	sudo nft delete table inet wdtt 2>/dev/null || true
	rm -rf "$work"
}
trap cleanup EXIT

server="${1:-}"
if [[ -z "$server" ]]; then
	server="$work/qwdtt"
	"$here/build-qwdtt.sh" "$server"
fi

git -c advice.detachedHead=false clone -q --depth=1 --branch "$CLIENT_REF" \
	https://github.com/SpaceNeuroX/proxy-turn-vk-android.git "$work/src"
cd "$work/src/go_client"
python3 - <<'PY'
p = 'creds.go'
s = open(p).read()
old = 'func GetCreds(ctx context.Context, link string, streamID int) (string, string, []string, error) {\n'
assert old in s, 'GetCreds signature changed upstream'
s = s.replace(old, old + '\tif u := os.Getenv("E2E_TURN_URL"); u != "" {\n\t\treturn "e2e", "e2e", []string{u}, nil\n\t}\n', 1)
if '"os"' not in s:
    s = s.replace('import (', 'import (\n\t"os"', 1)
open(p, 'w').write(s)
PY
mkdir -p turnsrv && cp "$here/qwdtt-e2e/turnsrv.go.txt" turnsrv/main.go
go mod tidy >/dev/null 2>&1
go build -o "$work/qclient" .
go build -o "$work/turnd" ./turnsrv
"$work/turnd" >"$work/turnd.log" 2>&1 &
turn_pid=$!

hostip="$(ip -4 -o route get 1.1.1.1 | sed -n 's/.* src \([0-9.]*\).*/\1/p')"
cd "$root"
go test -c -tags qwdtt_e2e -o "$work/qwdtt.e2e" ./qwdtt
sudo -E env QWDTT_E2E_BIN="$server" QWDTT_E2E_CLIENT="$work/qclient" \
	QWDTT_E2E_TURN=127.0.0.1:3478 QWDTT_E2E_HOSTIP="$hostip" \
	"$work/qwdtt.e2e" -test.v -test.run E2E
