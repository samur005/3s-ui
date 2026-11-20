package wgserver

import (
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"strings"
)

// parseStats reads the engine's "get" output into per-peer counters.
func parseStats(text string) []PeerStat {
	var out []PeerStat
	var cur *PeerStat
	for _, line := range strings.Split(text, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "public_key":
			raw, err := hex.DecodeString(v)
			if err != nil {
				cur = nil
				continue
			}
			out = append(out, PeerStat{PublicKey: base64.StdEncoding.EncodeToString(raw)})
			cur = &out[len(out)-1]
		case "rx_bytes":
			if cur != nil {
				cur.Rx, _ = strconv.ParseInt(v, 10, 64)
			}
		case "tx_bytes":
			if cur != nil {
				cur.Tx, _ = strconv.ParseInt(v, 10, 64)
			}
		case "last_handshake_time_sec":
			if cur != nil {
				cur.LastHandshake, _ = strconv.ParseInt(v, 10, 64)
			}
		}
	}
	return out
}
