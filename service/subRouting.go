package service

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/samur005/3s-ui/logger"
)

// Routing profile for the Happ-family "Routing" subscription header and the
// Incy routing line. The setting holds one of:
//
//   - a routing JSON object       -> sent as happ://routing/onadd/<base64>
//   - a happ://routing/... link   -> validated and sent as is
//   - an https:// URL             -> fetched (cached), its answer is one of the above
//
// Header values are capped at 8 KiB; a remote answer at 16 KiB.

const (
	subRoutingMaxValue  = 8 << 10
	subRoutingMaxBody   = 16 << 10
	subRoutingCacheTTL  = 10 * time.Minute
	subRoutingRetry     = 30 * time.Second
	subRoutingFetchTime = 6 * time.Second
)

var errRoutingRemote = errors.New("remote source")

// NormalizeHappRouting turns an inline routing value into the header value.
// With allowRemote an https URL is accepted (and reported as errRoutingRemote
// internally by resolveRouting); Save calls it with allowRemote=true, so a URL
// passes validation without being fetched from the settings form.
func NormalizeHappRouting(raw string, allowRemote bool) (string, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", nil
	}
	if strings.HasPrefix(strings.ToLower(text), "https://") || strings.HasPrefix(strings.ToLower(text), "http://") {
		if !allowRemote {
			return "", errors.New("a remote routing source must answer with routing JSON or a happ:// link, not another URL")
		}
		u, err := url.Parse(text)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return "", errors.New("a remote routing source must be an https URL")
		}
		return "", errRoutingRemote
	}
	return normalizeHappRoutingContent([]byte(text))
}

func normalizeHappRoutingContent(body []byte) (string, error) {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return "", errors.New("empty routing")
	}
	if strings.HasPrefix(text, "{") {
		compact, err := compactJSONObject([]byte(text))
		if err != nil {
			return "", fmt.Errorf("invalid routing JSON: %v", err)
		}
		out := "happ://routing/onadd/" + base64.StdEncoding.EncodeToString(compact)
		if len(out) > subRoutingMaxValue {
			return "", errors.New("routing is too large for a header (8 KiB)")
		}
		return out, nil
	}
	if text == "happ://routing/off" {
		return text, nil
	}
	if strings.ContainsAny(text, "\r\n") {
		return "", errors.New("a routing deeplink must be a single line")
	}
	var payload string
	for _, prefix := range []string{"happ://routing/onadd/", "happ://routing/add/"} {
		if rest, ok := strings.CutPrefix(text, prefix); ok {
			payload = rest
			break
		}
	}
	if payload == "" {
		return "", errors.New("expected routing JSON, a happ://routing/ link or an https URL")
	}
	decoded, err := decodeAnyBase64(payload)
	if err != nil {
		return "", fmt.Errorf("invalid routing payload: %v", err)
	}
	if _, err := compactJSONObject(decoded); err != nil {
		return "", fmt.Errorf("invalid routing payload JSON: %v", err)
	}
	if len(text) > subRoutingMaxValue {
		return "", errors.New("routing is too large for a header (8 KiB)")
	}
	return text, nil
}

func compactJSONObject(raw []byte) ([]byte, error) {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, errors.New("expected a JSON object")
	}
	return json.Marshal(obj)
}

func decodeAnyBase64(v string) ([]byte, error) {
	v = strings.TrimSpace(v)
	var last error
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		b, err := enc.DecodeString(v)
		if err == nil {
			return b, nil
		}
		last = err
	}
	return nil, last
}

type routingCacheEntry struct {
	content   string
	fetchedAt time.Time
	retryAt   time.Time
}

var (
	routingCacheMu sync.Mutex
	routingCache   = map[string]*routingCacheEntry{}
	// routingFetch is swapped by tests.
	routingFetch = fetchRemoteRouting
)

// ResolveHappRouting returns the Routing header value for the setting, or ""
// when there is none / the remote source has never answered. A remote source
// that fails keeps serving its last good answer.
func ResolveHappRouting(raw string) string {
	out, err := NormalizeHappRouting(raw, true)
	if err == nil {
		return out
	}
	if !errors.Is(err, errRoutingRemote) {
		return ""
	}
	src := strings.TrimSpace(raw)
	now := time.Now()
	routingCacheMu.Lock()
	entry := routingCache[src]
	if entry != nil && (now.Sub(entry.fetchedAt) < subRoutingCacheTTL || now.Before(entry.retryAt)) {
		routingCacheMu.Unlock()
		return entry.content
	}
	routingCacheMu.Unlock()

	content, ferr := routingFetch(src)
	routingCacheMu.Lock()
	defer routingCacheMu.Unlock()
	if entry == nil {
		entry = &routingCacheEntry{}
		routingCache[src] = entry
	}
	if ferr != nil {
		logger.Warning("sub: routing source ", src, ": ", ferr)
		entry.retryAt = now.Add(subRoutingRetry)
		return entry.content
	}
	entry.content = content
	entry.fetchedAt = now
	return content
}

func fetchRemoteRouting(src string) (string, error) {
	client := &http.Client{
		Timeout: subRoutingFetchTime,
		Transport: &http.Transport{
			Proxy:               nil,
			DialContext:         guardedDialContext(false, subRoutingFetchTime),
			TLSHandshakeTimeout: subRoutingFetchTime,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 || req.URL.Scheme != "https" {
				return errors.New("redirect refused")
			}
			return nil
		},
	}
	resp, err := client.Get(src)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, subRoutingMaxBody+1))
	if err != nil {
		return "", err
	}
	if len(body) > subRoutingMaxBody {
		return "", errors.New("response larger than 16 KiB")
	}
	return normalizeHappRoutingContent(body)
}

// IncyRoutingLine is the line appended to the plain link subscription for
// Incy: an https source becomes incy://autorouting/onadd/<url>, an incy://
// link is passed through.
func IncyRoutingLine(raw string) string {
	text := strings.TrimSpace(raw)
	switch {
	case text == "":
		return ""
	case strings.HasPrefix(text, "incy://"):
		return text
	case strings.HasPrefix(strings.ToLower(text), "https://"):
		return "incy://autorouting/onadd/" + text
	}
	return ""
}
