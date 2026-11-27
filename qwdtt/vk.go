package qwdtt

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/logger"
)

// The VK call API as the VK web client drives it: session cookies are
// exchanged for an access token, calls.start creates a call whose join link
// carries the hash, and calls.getCallPreview tells whether a call still runs.
const (
	vkAppID      = "6287487"
	vkAPIVersion = "5.280"
	vkWebHost    = "vk.ru"
	vkUserAgent  = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36"
	vkAuthCookie = "remixsid"
)

var vkHTTP = &http.Client{Timeout: 30 * time.Second}

// vkPost is replaced in tests.
var vkPost = func(endpoint string, form url.Values, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", vkUserAgent)
	req.Header.Set("Origin", "https://"+vkWebHost)
	req.Header.Set("Referer", "https://"+vkWebHost+"/")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := vkHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

// vkTransportError marks failures that say nothing about the call itself
// (network down, TLS, timeout). The liveness check must not read them as
// "call ended".
type vkTransportError struct{ err error }

func (e vkTransportError) Error() string { return e.err.Error() }
func (e vkTransportError) Unwrap() error { return e.err }

func vkParseAPIError(body []byte) error {
	var w struct {
		Error struct {
			Code    int    `json:"error_code"`
			Message string `json:"error_msg"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &w) != nil || w.Error.Code == 0 {
		return nil
	}
	return fmt.Errorf("VK API %d: %s", w.Error.Code, w.Error.Message)
}

func truncate(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// --- cookies -------------------------------------------------------------

type cookieEntry struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ParseCookies accepts what a user pastes: a JSON array of {"name","value"}
// objects (browser export, WhitelistBypass.Creator) or a raw Cookie header
// ("remixsid=...; remixlang=0"). It returns the canonical JSON to store and
// refuses input without the remixsid session cookie.
func ParseCookies(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("cookies are empty")
	}
	var cookies []cookieEntry
	if strings.HasPrefix(raw, "[") {
		if err := json.Unmarshal([]byte(raw), &cookies); err != nil {
			return "", fmt.Errorf("invalid cookies JSON: %w", err)
		}
	} else {
		raw = strings.TrimPrefix(raw, "Cookie:")
		for _, part := range strings.Split(raw, ";") {
			kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
			if len(kv) != 2 || strings.TrimSpace(kv[0]) == "" {
				continue
			}
			cookies = append(cookies, cookieEntry{Name: strings.TrimSpace(kv[0]), Value: strings.TrimSpace(kv[1])})
		}
	}
	clean := cookies[:0]
	hasAuth := false
	for _, c := range cookies {
		c.Name = strings.TrimSpace(c.Name)
		if c.Name == "" {
			continue
		}
		// Cookie values are forwarded in a header: refuse anything that could
		// split it.
		if strings.ContainsAny(c.Name+c.Value, "\r\n;") {
			return "", errors.New("cookies contain invalid characters")
		}
		if c.Name == vkAuthCookie && strings.TrimSpace(c.Value) != "" {
			hasAuth = true
		}
		clean = append(clean, c)
	}
	if len(clean) == 0 {
		return "", errors.New("cookies are empty")
	}
	if !hasAuth {
		return "", fmt.Errorf("no %s cookie: log in to VK and export the cookies again", vkAuthCookie)
	}
	b, _ := json.Marshal(clean)
	return string(b), nil
}

func cookieHeader(cookiesJSON string) (string, error) {
	if strings.TrimSpace(cookiesJSON) == "" {
		return "", errors.New("VK cookies are not set")
	}
	var cookies []cookieEntry
	if err := json.Unmarshal([]byte(cookiesJSON), &cookies); err != nil {
		return "", fmt.Errorf("stored VK cookies are unreadable: %w", err)
	}
	parts := make([]string, 0, len(cookies))
	for _, c := range cookies {
		if c.Name != "" {
			parts = append(parts, c.Name+"="+c.Value)
		}
	}
	if len(parts) == 0 {
		return "", errors.New("VK cookies are empty")
	}
	return strings.Join(parts, "; "), nil
}

// SaveCookies validates and stores VK cookies.
func SaveCookies(raw string) error {
	canon, err := ParseCookies(raw)
	if err != nil {
		return err
	}
	storeMu.Lock()
	defer storeMu.Unlock()
	db := database.GetDB()
	row, err := loadRow(db)
	if err != nil {
		return err
	}
	row.VkCookies = canon
	return saveRow(db, row)
}

// ClearCookies deletes the stored VK cookies.
func ClearCookies() error {
	storeMu.Lock()
	defer storeMu.Unlock()
	db := database.GetDB()
	row, err := loadRow(db)
	if err != nil {
		return err
	}
	row.VkCookies = ""
	return saveRow(db, row)
}

// LoadCookies returns the stored VK cookies JSON.
func LoadCookies() (string, error) {
	storeMu.Lock()
	defer storeMu.Unlock()
	db := database.GetDB()
	row, err := loadRow(db)
	if err != nil {
		return "", err
	}
	return row.VkCookies, nil
}

// HasCookies reports whether cookies are stored. The values themselves never
// leave the backend.
func HasCookies() bool {
	row, err := loadRow(database.GetDB())
	return err == nil && strings.TrimSpace(row.VkCookies) != ""
}

func storedCookieHeader() (string, error) {
	row, err := loadRow(database.GetDB())
	if err != nil {
		return "", err
	}
	return cookieHeader(row.VkCookies)
}

// --- VK API --------------------------------------------------------------

func vkWebToken(cookies string) (string, error) {
	body, err := vkPost("https://login."+vkWebHost+"/?act=web_token",
		url.Values{"version": {"1"}, "app_id": {vkAppID}},
		map[string]string{"Cookie": cookies})
	if err != nil {
		return "", vkTransportError{fmt.Errorf("web_token: %w", err)}
	}
	var tok struct {
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", fmt.Errorf("web_token parse: %w", err)
	}
	if tok.Data.AccessToken == "" {
		return "", fmt.Errorf("VK returned no token (cookies expired?): %s", truncate(body, 200))
	}
	return tok.Data.AccessToken, nil
}

func vkAPICall(token, method string, form url.Values) ([]byte, error) {
	form.Set("v", vkAPIVersion)
	body, err := vkPost("https://api."+vkWebHost+"/method/"+method, form,
		map[string]string{"Authorization": "Bearer " + token})
	if err != nil {
		return nil, vkTransportError{fmt.Errorf("%s: %w", method, err)}
	}
	if err := vkParseAPIError(body); err != nil {
		return nil, err
	}
	return body, nil
}

func vkCurrentUserID(token string) (string, error) {
	body, err := vkAPICall(token, "users.get", url.Values{})
	if err != nil {
		return "", err
	}
	var r struct {
		Response []struct {
			ID int64 `json:"id"`
		} `json:"response"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", fmt.Errorf("users.get parse: %w", err)
	}
	if len(r.Response) == 0 || r.Response[0].ID == 0 {
		return "", fmt.Errorf("users.get: empty id: %s", truncate(body, 200))
	}
	return fmt.Sprint(r.Response[0].ID), nil
}

// CreatedCall is a VK call made through calls.start.
type CreatedCall struct {
	CallID   string `json:"callId"`
	JoinLink string `json:"joinLink"`
	Hash     string `json:"hash"`
	Started  int64  `json:"startedAt"`
	// Remote marks a call a WDTT panel created (remote.go): it is not this
	// panel's to end.
	Remote bool `json:"remote,omitempty"`
}

// createCall starts a call on the account the cookies belong to.
func createCall(cookies string) (CreatedCall, error) {
	var out CreatedCall
	token, err := vkWebToken(cookies)
	if err != nil {
		return out, err
	}
	peer, err := vkCurrentUserID(token)
	if err != nil {
		return out, err
	}
	body, err := vkAPICall(token, "calls.start", url.Values{"peer_id": {peer}})
	if err != nil {
		return out, err
	}
	var r struct {
		Response struct {
			CallID   string `json:"call_id"`
			JoinLink string `json:"join_link"`
		} `json:"response"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return out, fmt.Errorf("calls.start parse: %w", err)
	}
	if r.Response.CallID == "" || r.Response.JoinLink == "" {
		return out, fmt.Errorf("calls.start: incomplete answer: %s", truncate(body, 200))
	}
	hash := StripHash(r.Response.JoinLink)
	if hash == "" {
		return out, fmt.Errorf("cannot read a hash from %q", r.Response.JoinLink)
	}
	return CreatedCall{CallID: r.Response.CallID, JoinLink: r.Response.JoinLink, Hash: hash, Started: time.Now().Unix()}, nil
}

// finishCall ends a call (best effort; an already ended call answers an error).
func finishCall(cookies, callID string) error {
	if strings.TrimSpace(callID) == "" {
		return errors.New("empty call id")
	}
	token, err := vkWebToken(cookies)
	if err != nil {
		return err
	}
	body, err := vkAPICall(token, "calls.forceFinish", url.Values{"call_id": {callID}})
	if err != nil {
		return err
	}
	var r struct {
		Response int `json:"response"`
	}
	if err := json.Unmarshal(body, &r); err != nil || r.Response != 1 {
		return fmt.Errorf("calls.forceFinish: unexpected answer: %s", truncate(body, 200))
	}
	return nil
}

func vkAnonToken() (string, error) {
	body, err := vkPost("https://login."+vkWebHost+"/?act=get_anonym_token",
		url.Values{"client_id": {vkAppID}}, nil)
	if err != nil {
		return "", vkTransportError{fmt.Errorf("get_anonym_token: %w", err)}
	}
	var tok struct {
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", err
	}
	if tok.Data.AccessToken == "" {
		return "", errors.New("VK returned no anonymous token")
	}
	return tok.Data.AccessToken, nil
}

// CallAlive asks VK whether the call behind a hash still runs. The error is
// non-nil when the answer could not be obtained (network, token); the result
// is only meaningful with a nil error.
func CallAlive(hash string) (bool, error) {
	hash = StripHash(hash)
	if hash == "" {
		return false, errors.New("empty hash")
	}
	token, err := vkAnonToken()
	if err != nil {
		return false, err
	}
	body, err := vkPost("https://api."+vkWebHost+"/method/calls.getCallPreview",
		url.Values{"v": {vkAPIVersion}, "vk_join_link": {JoinURLBase + hash}},
		map[string]string{"Authorization": "Bearer " + token})
	if err != nil {
		return false, vkTransportError{fmt.Errorf("calls.getCallPreview: %w", err)}
	}
	// An API error here is VK saying the link is not a live call.
	if vkParseAPIError(body) != nil {
		return false, nil
	}
	var r struct {
		Response struct {
			OKJoinLink string `json:"ok_join_link"`
		} `json:"response"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return false, fmt.Errorf("calls.getCallPreview parse: %w", err)
	}
	return r.Response.OKJoinLink != "", nil
}

// CheckCookies verifies the stored cookies still open a VK session.
func CheckCookies() error {
	h, err := storedCookieHeader()
	if err != nil {
		return err
	}
	token, err := vkWebToken(h)
	if err != nil {
		return err
	}
	_, err = vkCurrentUserID(token)
	return err
}

// --- calls the panel created ---------------------------------------------

func loadCalls() ([]CreatedCall, error) {
	row, err := loadRow(database.GetDB())
	if err != nil {
		return nil, err
	}
	var calls []CreatedCall
	if strings.TrimSpace(row.VkCalls) != "" {
		_ = json.Unmarshal([]byte(row.VkCalls), &calls)
	}
	return calls, nil
}

func saveCalls(calls []CreatedCall) error {
	storeMu.Lock()
	defer storeMu.Unlock()
	db := database.GetDB()
	row, err := loadRow(db)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(calls)
	row.VkCalls = string(b)
	return saveRow(db, row)
}

// hashGenMu keeps a manual "generate" and the cron job from each creating a
// call for the same empty slot.
var hashGenMu sync.Mutex

// GenerateHash creates a VK call and adds its hash to the configuration. With
// replaceOldest and a full list (MaxHashes) the oldest hash is dropped; its
// call is ended when the panel created it. The updated configuration is
// returned; the caller applies it.
func GenerateHash(replaceOldest bool) (Config, CreatedCall, error) {
	hashGenMu.Lock()
	defer hashGenMu.Unlock()
	return generateLocked(replaceOldest, "")
}

func generateLocked(replaceOldest bool, replaceHash string) (Config, CreatedCall, error) {
	if EnvHashOverride() != "" {
		return Config{}, CreatedCall{}, fmt.Errorf("%s is set; the hash list is managed through the environment", EnvVkHash)
	}
	// Own VK cookies first; a remote WDTT panel otherwise.
	var cookies string
	var call CreatedCall
	var err error
	if HasCookies() {
		if cookies, err = storedCookieHeader(); err != nil {
			return Config{}, CreatedCall{}, err
		}
		call, err = createCall(cookies)
	} else if rs := LoadRemote(); strings.TrimSpace(rs.URL) != "" {
		call, err = remoteCreateHash(rs)
	} else {
		err = errors.New("no hash source: store VK cookies or set a WDTT panel")
	}
	if err != nil {
		return Config{}, CreatedCall{}, err
	}
	var dropped, replaced string
	cfg, err := update(func(c *Config) error {
		list := ParseHashes(c.VkHashes)
		if replaceHash != "" {
			for i, h := range list {
				if h == replaceHash {
					list[i] = call.Hash
					c.VkHashes = strings.Join(list, ",")
					replaced = replaceHash
					return nil
				}
			}
		}
		if len(list) >= MaxHashes {
			if !replaceOldest {
				return fmt.Errorf("already %d hashes; remove one first", MaxHashes)
			}
			dropped = list[0]
			list = list[1:]
		}
		list = append(list, call.Hash)
		c.VkHashes = strings.Join(list, ",")
		return nil
	})
	if err != nil {
		// The call exists but is not wanted: do not leave it running.
		if !call.Remote {
			_ = finishCall(cookies, call.CallID)
		}
		return cfg, call, err
	}
	calls, _ := loadCalls()
	calls = append(calls, call)
	if replaced != "" {
		// The old call ended on its own; forget it without a VK round trip.
		kept := calls[:0]
		for _, c := range calls {
			if c.Hash != replaced {
				kept = append(kept, c)
			}
		}
		calls = kept
	}
	if dropped != "" {
		calls = finishDropped(cookies, calls, dropped)
	}
	// Keep the bookkeeping bounded.
	if len(calls) > 32 {
		calls = calls[len(calls)-32:]
	}
	if err := saveCalls(calls); err != nil {
		logger.Warning("qwdtt: saving call list: ", err)
	}
	return cfg, call, nil
}

func finishDropped(cookies string, calls []CreatedCall, hash string) []CreatedCall {
	out := calls[:0]
	for _, c := range calls {
		if c.Hash == hash {
			if !c.Remote && cookies != "" {
				_ = finishCall(cookies, c.CallID)
			}
			continue
		}
		out = append(out, c)
	}
	return out
}

// deadStreak counts consecutive "call ended" answers per hash; a hash is only
// replaced after two in a row so one odd VK answer does not churn the links.
var (
	deadMu     sync.Mutex
	deadStreak = map[string]int{}
)

const deadThreshold = 2

// HashStatus is the liveness of one configured hash.
type HashStatus struct {
	Hash      string `json:"hash"`
	Alive     *bool  `json:"alive"` // nil: could not be checked
	Generated bool   `json:"generated"`
	Error     string `json:"error,omitempty"`
}

// CheckHashes asks VK about every configured hash.
func CheckHashes(cfg Config) []HashStatus {
	calls, _ := loadCalls()
	ours := map[string]bool{}
	for _, c := range calls {
		ours[c.Hash] = true
	}
	var out []HashStatus
	for _, h := range ParseHashes(cfg.EffectiveHashes()) {
		st := HashStatus{Hash: h, Generated: ours[h]}
		alive, err := CallAlive(h)
		if err != nil {
			st.Error = err.Error()
		} else {
			st.Alive = &alive
		}
		out = append(out, st)
	}
	return out
}

// MaintainHashes is the cron entry point. It fills an empty hash list and
// replaces hashes whose call has ended, when AutoVkHash is on and cookies are
// stored. It reports whether the configuration changed.
func MaintainHashes() (changed bool, err error) {
	cfg, err := Load()
	if err != nil {
		return false, err
	}
	if !cfg.Enabled || !cfg.AutoVkHash || EnvHashOverride() != "" {
		return false, nil
	}
	if !CanGenerate() {
		return false, nil
	}
	hashGenMu.Lock()
	defer hashGenMu.Unlock()

	list := ParseHashes(cfg.VkHashes)
	if len(list) == 0 {
		if _, _, err := generateLocked(false, ""); err != nil {
			return false, err
		}
		return true, nil
	}
	for _, h := range list {
		alive, cerr := CallAlive(h)
		if cerr != nil {
			continue // unknown: keep the hash
		}
		deadMu.Lock()
		if alive {
			delete(deadStreak, h)
			deadMu.Unlock()
			continue
		}
		deadStreak[h]++
		n := deadStreak[h]
		deadMu.Unlock()
		if n < deadThreshold {
			continue
		}
		logger.Infof("qwdtt: VK call for hash %s has ended, creating a new one", h)
		if _, _, err := generateLocked(false, h); err != nil {
			return changed, err
		}
		deadMu.Lock()
		delete(deadStreak, h)
		deadMu.Unlock()
		changed = true
	}
	return changed, nil
}

// EnsureHashesNow creates a hash right away when none is configured; used
// after the operator enables qWDTT or saves cookies.
func EnsureHashesNow() (bool, error) {
	cfg, err := Load()
	if err != nil {
		return false, err
	}
	if !cfg.AutoVkHash || EnvHashOverride() != "" || len(ParseHashes(cfg.VkHashes)) > 0 || !CanGenerate() {
		return false, nil
	}
	hashGenMu.Lock()
	defer hashGenMu.Unlock()
	if _, _, err := generateLocked(false, ""); err != nil {
		return false, err
	}
	return true, nil
}

// CanGenerate reports whether the panel has a way to create hashes: its own
// VK cookies or a remote WDTT panel.
func CanGenerate() bool { return HasCookies() || HasRemote() }


// EndCallByHash ends the VK call that owns the given hash, if this panel created it.
func EndCallByHash(hash string) error {
	hash = StripHash(hash)
	if hash == "" {
		return errors.New("empty hash")
	}
	calls, err := loadCalls()
	if err != nil {
		return err
	}
	var found *CreatedCall
	var rest []CreatedCall
	for i := range calls {
		if calls[i].Hash == hash && !calls[i].Remote {
			found = &calls[i]
		} else {
			rest = append(rest, calls[i])
		}
	}
	if found == nil {
		return errors.New("call not found or not owned by this panel")
	}
	cookies, err := LoadCookies()
	if err != nil || cookies == "" {
		return errors.New("no cookies")
	}
	if err := finishCall(cookies, found.CallID); err != nil {
		return err
	}
	return saveCalls(rest)
}

// RemoveHash removes a hash from the qWDTT config.
func RemoveHash(hash string) error {
	hash = StripHash(hash)
	if hash == "" {
		return errors.New("empty hash")
	}
	cfg, err := Load()
	if err != nil {
		return err
	}
	parts := ParseHashes(cfg.VkHashes)
	var kept []string
	for _, h := range parts {
		if h != hash {
			kept = append(kept, h)
		}
	}
	cfg.VkHashes = strings.Join(kept, ",")
	_, err = Save(cfg)
	return err
}
