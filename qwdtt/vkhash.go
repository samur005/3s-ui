package qwdtt

import "strings"

// MaxHashes is how many hashes one share link carries.
const MaxHashes = 4

// JoinURLBase is the public join link of a VK call; the hash is its last part.
const JoinURLBase = "https://vk.ru/call/join/"

// StripHash extracts the bare token from a hash or a .../call/join/<hash> link
// (vk.com, vk.ru and their m. hosts, and vk.me/join/). Other URLs give "".
func StripHash(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	lower := strings.ToLower(s)
	if i := strings.Index(lower, "/call/join/"); i >= 0 {
		s = s[i+len("/call/join/"):]
	} else if i := strings.Index(lower, "vk.me/join/"); i >= 0 {
		s = s[i+len("vk.me/join/"):]
	} else if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return ""
	}
	if i := strings.IndexAny(s, "?#/"); i >= 0 {
		s = s[:i]
	}
	return strings.Trim(s, "/ ")
}

// ParseHashes splits a list (comma, semicolon or whitespace separated) into
// distinct bare hashes, at most MaxHashes of them.
func ParseHashes(raw string) []string {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	}) {
		h := StripHash(part)
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
		if len(out) >= MaxHashes {
			break
		}
	}
	return out
}

// NormalizeHashes is ParseHashes joined with commas, the stored form.
func NormalizeHashes(raw string) string {
	return strings.Join(ParseHashes(raw), ",")
}
