package sub

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/samur005/3s-ui/database/model"
	"github.com/samur005/3s-ui/service"
)

// Subscription profile headers beyond the three util.GetHeaders builds:
// Profile-Title override, Support-Url, Profile-Web-Page-Url, Announce,
// Routing-Enable, Routing, Hide-Settings, the Happ set and the X-Hwid-* set.
// The header names and value formats follow what the clients read (and what
// LucX's subscription server emits); the code is this panel's own.

var happUA = regexp.MustCompile(`(?i)\bhapp\b`)

// IsHappClient reports whether a User-Agent is Happ's.
func IsHappClient(ua string) bool { return happUA.MatchString(ua) }

var placeholderRe = regexp.MustCompile(`\{\{([A-Z_]+)\}\}`)

// renderPlaceholders expands {{EMAIL}} / {{USERNAME}} / {{NAME}} (the client
// name, which is also its subscription id here), {{SUB_ID}}, {{ID}},
// {{SHORT_ID}}, {{TELEGRAM_ID}}, {{REMARK}} and {{GROUP}}. Unknown tokens are
// left as typed. escape URL-encodes the values (for URL settings).
func renderPlaceholders(v string, c *model.Client, escape bool) string {
	if c == nil || !strings.Contains(v, "{{") {
		return v
	}
	return placeholderRe.ReplaceAllStringFunc(v, func(m string) string {
		var out string
		switch m[2 : len(m)-2] {
		case "EMAIL", "USERNAME", "NAME", "SUB_ID":
			out = c.Name
		case "ID":
			out = strconv.FormatUint(uint64(c.Id), 10)
		case "SHORT_ID":
			out = c.Name
			if len(out) > 8 {
				out = out[:8]
			}
		case "TELEGRAM_ID":
			if c.TgId != 0 {
				out = strconv.FormatInt(c.TgId, 10)
			}
		case "REMARK":
			out = c.Remark
		case "GROUP":
			out = c.Group
		default:
			return m
		}
		if escape {
			return url.QueryEscape(out)
		}
		return out
	})
}

func singleLine(v string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, v))
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// encodeTitle sends a non-ASCII title as "base64:..." -- the form Happ,
// v2RayTun, Hiddify, Streisand and Clash Verge decode -- since raw UTF-8 in a
// header is read as Latin-1 by many HTTP stacks. ASCII stays readable.
func encodeTitle(t string) string {
	if t == "" || isASCII(t) || strings.HasPrefix(t, "base64:") {
		return t
	}
	return "base64:" + base64.StdEncoding.EncodeToString([]byte(t))
}

// profileContext is what one response's extra headers depend on.
type profileContext struct {
	cfg        service.SubProfileConfig
	client     *model.Client
	userAgent  string
	builtinURL string // the subscriber dashboard URL of this client
}

// buildProfileHeaders computes the extra headers. Pure apart from the
// routing resolver (remote sources are cached), so it is tested directly.
func buildProfileHeaders(p profileContext) http.Header {
	h := http.Header{}
	cfg := p.cfg
	if t := singleLine(renderPlaceholders(cfg.Title, p.client, false)); t != "" {
		h.Set("Profile-Title", encodeTitle(t))
	}
	if v := singleLine(renderPlaceholders(cfg.SupportURL, p.client, true)); v != "" {
		h.Set("Support-Url", v)
	}
	switch cfg.ProfileMode {
	case service.SubProfileModeBuiltin:
		if p.builtinURL != "" {
			h.Set("Profile-Web-Page-Url", p.builtinURL)
		}
	case service.SubProfileModeCustom:
		if v := singleLine(renderPlaceholders(cfg.ProfileURL, p.client, true)); v != "" {
			h.Set("Profile-Web-Page-Url", v)
		}
	}
	if a := strings.TrimSpace(renderPlaceholders(cfg.Announce, p.client, false)); a != "" {
		h.Set("Announce", "base64:"+base64.StdEncoding.EncodeToString([]byte(a)))
	}

	// Send the configured Happ headers to every client, not only when the
	// User-Agent says Happ. AntiBS2client and other apps never match the
	// Happ regex, so they used to get none of these.
	happManaged := cfg.Happ.AutoDetect
	if cfg.EnableRouting {
		h.Set("Routing-Enable", "true")
	} else if happManaged {
		h.Set("Routing-Enable", "0")
	}
	if r := service.ResolveHappRouting(cfg.RoutingRules); r != "" {
		h.Set("Routing", r)
	}
	if cfg.HideSettings {
		h.Set("Hide-Settings", "1")
	} else if happManaged {
		h.Set("Hide-Settings", "0")
	}
	if happManaged {
		applyHappHeaders(h, cfg.Happ)
	}
	return h
}

func applyHappHeaders(h http.Header, c service.HappConfig) {
	set := func(k, v string) {
		if v = singleLine(v); v != "" {
			h.Set(k, v)
		}
	}
	set("ProviderID", c.ProviderID)
	set("New-Url", c.NewURL)
	set("Fallback-Url", c.FallbackURL)
	if text := singleLine(c.SubInfoText); text != "" {
		color := strings.ToLower(strings.TrimSpace(c.SubInfoColor))
		switch color {
		case "", "primary", "info":
			color = "blue"
		case "success":
			color = "green"
		case "warning", "danger":
			color = "red"
		}
		h.Set("Sub-Info-Color", color)
		h.Set("Sub-Info-Text", text)
		set("Sub-Info-Button-Text", c.SubInfoButtonText)
		set("Sub-Info-Button-Link", c.SubInfoButtonLink)
	}
	if c.SubExpire {
		h.Set("Sub-Expire", "1")
		set("Sub-Expire-Button-Link", c.SubExpireButtonLink)
	}
	if c.NotificationExpire {
		h.Set("Notification-Subs-Expire", "1")
	}
	if c.NoLimit {
		h.Set("No-Limit-Enabled", "1")
	}
	if c.AlwaysHwid {
		h.Set("Subscription-Always-Hwid-Enable", "1")
	}
	set("Tun-Mode", c.TunMode)
	set("Tun-Type", c.TunType)
	set("Exclude-Routes", c.ExcludeRoutes)
	if c.ExcludeApns {
		h.Set("Exclude-Apns-Enable", "true")
	}
	set("Color-Profile", c.ColorProfile)
	if ping := singleLine(c.PingType); ping != "" {
		if strings.EqualFold(ping, "http") {
			ping = "proxy"
		}
		h.Set("Ping-Type", ping)
	}
	if c.AutoConnect {
		h.Set("Subscription-Autoconnect", "1")
		t := strings.ToLower(singleLine(c.AutoConnectType))
		switch t {
		case "fastest":
			t = "lowestdelay"
		case "last":
			t = "lastused"
		}
		if t != "" {
			h.Set("Subscription-Autoconnect-Type", t)
		}
	}
	if mode := singleLine(c.LocalProxyAuth); mode != "" {
		h.Set("Socks-Auth-Mode", mode)
		h.Set("Http-Auth-Mode", mode)
	}
	if mode := strings.ToLower(singleLine(c.PerAppMode)); mode != "" && mode != "off" {
		switch mode {
		case "include":
			mode = "on"
		case "exclude":
			mode = "bypass"
		}
		h.Set("Per-App-Proxy-Mode", mode)
		set("Per-App-Proxy-List", c.PerAppList)
	}
}

// hwidHeaders are the X-Hwid-* flags for the app.
func hwidHeaders(h http.Header, r service.HwidGateResult) {
	if r.Active {
		h.Set("X-Hwid-Active", "true")
	}
	if r.NotSupported {
		h.Set("X-Hwid-Not-Supported", "true")
	}
	if r.LimitReached {
		h.Set("X-Hwid-Limit", "true")
	}
	if r.MaxDevicesReached {
		h.Set("X-Hwid-Max-Devices-Reached", "true")
	}
}
