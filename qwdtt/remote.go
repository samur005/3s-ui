package qwdtt

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/samur005/3s-ui/database"
)

// A remote WDTT panel as a VK hash source.
//
// The LucX fork (samur005/lucx-ui-samur005, docs/VKHASH.md) fills an empty
// hash list from a WDTT panel (ildarmaga/wdtt) that holds the VK login: a POST
// to <base>/panel/api/vk/call/create answers {"success":true,"obj":{"vk_hash":
// ...}}. This is the same source, written anew here, for a panel that keeps no
// VK cookies of its own -- typically a node whose master's WDTT panel already
// has them. Own cookies, when stored, win: they need no third party.
//
// The WDTT panel authenticates with a session cookie and, on every POST, an
// X-CSRF-Token header equal to its wdtt-csrf cookie (panel/csrf.go); both
// come back from POST <base>/login. A bearer token is sent as such for a
// fronting proxy that checks one.

// Environment overrides of the stored remote source. The LUCX_ names are read
// too, so an /etc/default file written for LucX works unchanged.
const (
	EnvWdttURL   = "S3UI_WDTT_URL"
	EnvWdttToken = "S3UI_WDTT_TOKEN"
	EnvWdttUser  = "S3UI_WDTT_USER"
	EnvWdttPass  = "S3UI_WDTT_PASS"
)

// RemoteSource is the WDTT panel hashes can be created through.
type RemoteSource struct {
	URL      string `json:"url"`
	Token    string `json:"token,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	// FromEnv is set when the environment supplied the URL.
	FromEnv bool `json:"-"`
}

func envFirst(names ...string) string {
	for _, n := range names {
		if v := strings.TrimSpace(os.Getenv(n)); v != "" {
			return v
		}
	}
	return ""
}

// LoadRemote returns the remote source: the environment when it names a URL,
// else the stored one.
func LoadRemote() RemoteSource {
	if u := envFirst(EnvWdttURL, "LUCX_WDTT_URL"); u != "" {
		return RemoteSource{
			URL:      u,
			Token:    envFirst(EnvWdttToken, "LUCX_WDTT_TOKEN"),
			Username: envFirst(EnvWdttUser, "LUCX_WDTT_USER"),
			Password: envFirst(EnvWdttPass, "LUCX_WDTT_PASS"),
			FromEnv:  true,
		}
	}
	row, err := loadRow(database.GetDB())
	if err != nil || strings.TrimSpace(row.Wdtt) == "" {
		return RemoteSource{}
	}
	var rs RemoteSource
	_ = json.Unmarshal([]byte(row.Wdtt), &rs)
	return rs
}

// HasRemote reports whether a remote source is configured.
func HasRemote() bool { return strings.TrimSpace(LoadRemote().URL) != "" }

// SaveRemote stores the remote source. An empty password or token keeps the
// stored one, so the page never has to send secrets back; an empty URL clears
// the whole source.
func SaveRemote(rs RemoteSource) error {
	rs.URL = strings.TrimRight(strings.TrimSpace(rs.URL), "/")
	if rs.URL != "" {
		u, err := url.Parse(rs.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("qwdtt: WDTT panel URL must be http(s)://host[:port][/path]")
		}
	}
	storeMu.Lock()
	defer storeMu.Unlock()
	db := database.GetDB()
	row, err := loadRow(db)
	if err != nil {
		return err
	}
	if rs.URL == "" {
		row.Wdtt = ""
		return saveRow(db, row)
	}
	var old RemoteSource
	_ = json.Unmarshal([]byte(row.Wdtt), &old)
	if rs.Password == "" && rs.Username == old.Username {
		rs.Password = old.Password
	}
	if rs.Token == "" {
		rs.Token = old.Token
	}
	b, _ := json.Marshal(rs)
	row.Wdtt = string(b)
	return saveRow(db, row)
}

// RemoteView is the remote source as the page shows it: no secrets.
type RemoteView struct {
	URL         string `json:"url"`
	Username    string `json:"username"`
	HasPassword bool   `json:"hasPassword"`
	HasToken    bool   `json:"hasToken"`
	FromEnv     bool   `json:"fromEnv"`
}

// View strips the secrets.
func (rs RemoteSource) View() RemoteView {
	return RemoteView{URL: rs.URL, Username: rs.Username, HasPassword: rs.Password != "", HasToken: rs.Token != "", FromEnv: rs.FromEnv}
}

var remoteHTTP = &http.Client{Timeout: 30 * time.Second}

// remoteLogin returns the cookie header and CSRF token of a WDTT session.
func remoteLogin(base, user, pass string) (cookie, csrf string, err error) {
	form := url.Values{"username": {user}, "password": {pass}}
	req, err := http.NewRequest(http.MethodPost, base+"/login", strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	resp, err := remoteHTTP.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var r struct {
		Success bool   `json:"success"`
		Msg     string `json:"msg"`
	}
	if json.Unmarshal(body, &r) == nil && !r.Success {
		return "", "", fmt.Errorf("WDTT login refused: %s", r.Msg)
	}
	var parts []string
	for _, c := range resp.Cookies() {
		parts = append(parts, c.Name+"="+c.Value)
		if c.Name == "wdtt-csrf" {
			csrf = c.Value
		}
	}
	if len(parts) == 0 {
		return "", "", fmt.Errorf("WDTT login: no session cookie (HTTP %d)", resp.StatusCode)
	}
	return strings.Join(parts, "; "), csrf, nil
}

// remoteCreateHash asks the WDTT panel to start a VK call and returns its hash.
func remoteCreateHash(rs RemoteSource) (CreatedCall, error) {
	base := strings.TrimRight(strings.TrimSpace(rs.URL), "/")
	if base == "" {
		return CreatedCall{}, errors.New("no WDTT panel configured")
	}
	var cookie, csrf string
	if rs.Username != "" {
		var err error
		if cookie, csrf, err = remoteLogin(base, rs.Username, rs.Password); err != nil {
			return CreatedCall{}, err
		}
	}
	body, _ := json.Marshal(map[string]any{"password": "", "apply": false})
	req, err := http.NewRequest(http.MethodPost, base+"/panel/api/vk/call/create", bytes.NewReader(body))
	if err != nil {
		return CreatedCall{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	if rs.Token != "" {
		req.Header.Set("Authorization", "Bearer "+rs.Token)
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, err := remoteHTTP.Do(req)
	if err != nil {
		return CreatedCall{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var w struct {
		Success bool   `json:"success"`
		Msg     string `json:"msg"`
		Obj     struct {
			VkHash   string `json:"vk_hash"`
			JoinLink string `json:"join_link"`
			Session  struct {
				CallID string `json:"call_id"`
			} `json:"session"`
		} `json:"obj"`
		VkHash string `json:"vk_hash"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		return CreatedCall{}, fmt.Errorf("WDTT answered HTTP %d: %s", resp.StatusCode, truncate(raw, 200))
	}
	h := w.Obj.VkHash
	if h == "" {
		h = w.VkHash
	}
	hash := StripHash(h)
	if hash == "" && w.Obj.JoinLink != "" {
		hash = StripHash(w.Obj.JoinLink)
	}
	if hash == "" {
		msg := w.Msg
		if msg == "" {
			msg = truncate(raw, 200)
		}
		return CreatedCall{}, fmt.Errorf("WDTT gave no hash: %s", msg)
	}
	// The call belongs to the WDTT panel's account: no call id is kept, so
	// this panel never tries to end it with cookies it does not have.
	return CreatedCall{JoinLink: w.Obj.JoinLink, Hash: hash, Started: time.Now().Unix(), Remote: true}, nil
}
