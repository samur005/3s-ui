#!/usr/bin/env bash
# End-to-end check of Master -> Remote Node management on one machine.
#
#   scripts/multinode-e2e/run.sh /path/to/3sui [/path/to/3sui-previous-release]
#
# Starts two throwaway panels (master :23095, node :22095) on loopback with
# their own databases in a temp dir, gives each an API token, and runs
# e2e.py against them. With a second binary (an older release) it also starts
# a legacy node (:25095) and an old master (:26095) and runs compat.py.
# The binary needs the frontend embedded (frontend/dist copied to web/html).
# Nothing outside the temp dir is touched; all processes are stopped on exit.
set -euo pipefail
BIN=$(realpath "$1"); OLD=${2:+$(realpath "$2")}
HERE=$(cd "$(dirname "$0")" && pwd)
WORK=$(mktemp -d)
pids=()
cleanup() { for p in "${pids[@]}"; do kill "$p" 2>/dev/null || true; done; rm -rf "$WORK"; }
trap cleanup EXIT
cd "$WORK"
cp "$HERE"/e2e.py "$HERE"/compat.py .

start() { # name binary port
  mkdir -p "$1"
  S3UI_DB_FOLDER=$WORK/$1 "$2" setting -port "$3" -path /app/ -subPort $(( $3 + 1 )) >/dev/null
  python3 - "$1" <<'PY'
import sqlite3, secrets, sys
r = sys.argv[1]; t = 'e2e-' + r + '-' + secrets.token_hex(12)
c = sqlite3.connect(f'{r}/3s-ui.db'); c.execute('insert into tokens(desc,token,expiry,user_id) values(?,?,0,1)', ('e2e', t)); c.commit()
open(f'{r}.tok', 'w').write(t)
PY
  S3UI_DB_FOLDER=$WORK/$1 "$2" >"$1.log" 2>&1 &
  pids+=($!)
}

start node "$BIN" 22095
start master "$BIN" 23095
if [ -n "$OLD" ]; then
  start legacy "$OLD" 25095
  start oldmaster "$OLD" 26095
fi
sleep 8
python3 e2e.py
[ -n "$OLD" ] && python3 compat.py
