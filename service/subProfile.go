package service

import (
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
	"github.com/samur005/3s-ui/util/common"

	"golang.org/x/net/http/httpguts"
)

// Subscription profile headers: the metadata VPN clients (Happ, v2RayTun,
// Hiddify, Streisand, Incy, Clash/mihomo...) read from a subscription
// response besides the body. Settings only; the sub server builds the headers
// (sub/profileHeaders.go). Same header set as LucX's subscription server,
// implemented independently.

// Profile web page modes (Profile-Web-Page-Url).
const (
	SubProfileModeNone    = "none"
	SubProfileModeBuiltin = "builtin" // the subscriber dashboard (?format=page)
	SubProfileModeCustom  = "custom"  // subProfileUrl
)

// subProfileDefaults are merged into defaultValueMap at init: Save only
// UPDATEs rows that exist, and GetAllSetting seeds a row for every default.
var subProfileDefaults = map[string]string{
	"subTitle":             "",
	"subSupportUrl":        "",
	"subProfileMode":       SubProfileModeNone,
	"subProfileUrl":        "",
	"subAnnounce":          "",
	"subEnableRouting":     "false",
	"subRoutingRules":      "",
	"subHideSettings":      "false",
	"subIncyEnableRouting": "false",
	"subIncyRoutingRules":  "",

	"subHappAutoDetect":          "false",
	"subHappProviderId":          "",
	"subHappNewUrl":              "",
	"subHappFallbackUrl":         "",
	"subHappSubInfoColor":        "blue",
	"subHappSubInfoText":         "",
	"subHappSubInfoButtonText":   "",
	"subHappSubInfoButtonLink":   "",
	"subHappSubExpire":           "false",
	"subHappSubExpireButtonLink": "",
	"subHappNotificationExpire":  "false",
	"subHappNoLimit":             "false",
	"subHappAlwaysHwid":          "false",
	"subHappTunMode":             "",
	"subHappTunType":             "",
	"subHappExcludeRoutes":       "",
	"subHappExcludeApns":         "false",
	"subHappColorProfile":        "",
	"subHappPingType":            "",
	"subHappAutoConnect":         "false",
	"subHappAutoConnectType":     "lowestdelay",
	"subHappPerAppMode":          "off",
	"subHappPerAppList":          "",
	"subHappLocalProxyAuth":      "",
}

func init() {
	for k, v := range subProfileDefaults {
		defaultValueMap[k] = v
	}
}

// SubProfileConfig is every profile-header setting, read in one query per
// subscription request so a saved change applies without a restart.
type SubProfileConfig struct {
	Title, SupportURL, ProfileMode, ProfileURL, Announce string
	EnableRouting                                        bool
	RoutingRules                                         string
	HideSettings                                         bool
	IncyEnableRouting                                    bool
	IncyRoutingRules                                     string
	Happ                                                 HappConfig
}

// HappConfig is the Happ-only header set, sent when AutoDetect is on and the
// request's User-Agent identifies Happ.
type HappConfig struct {
	AutoDetect          bool
	ProviderID          string
	NewURL              string
	FallbackURL         string
	SubInfoColor        string
	SubInfoText         string
	SubInfoButtonText   string
	SubInfoButtonLink   string
	SubExpire           bool
	SubExpireButtonLink string
	NotificationExpire  bool
	NoLimit             bool
	AlwaysHwid          bool
	TunMode             string
	TunType             string
	ExcludeRoutes       string
	ExcludeApns         bool
	ColorProfile        string
	PingType            string
	AutoConnect         bool
	AutoConnectType     string
	PerAppMode          string
	PerAppList          string
	LocalProxyAuth      string
}

func (s *SettingService) GetSubProfileConfig() (SubProfileConfig, error) {
	keys := make([]string, 0, len(subProfileDefaults))
	for k := range subProfileDefaults {
		keys = append(keys, k)
	}
	v := make(map[string]string, len(keys))
	for k, d := range subProfileDefaults {
		v[k] = d
	}
	var rows []model.Setting
	if err := database.GetDB().Model(model.Setting{}).Where("key in ?", keys).Find(&rows).Error; err != nil {
		return SubProfileConfig{}, err
	}
	for _, r := range rows {
		v[r.Key] = r.Value
	}
	b := func(k string) bool { ok, _ := strconv.ParseBool(v[k]); return ok }
	mode := v["subProfileMode"]
	if mode == "" && strings.TrimSpace(v["subProfileUrl"]) != "" {
		mode = SubProfileModeCustom
	}
	return SubProfileConfig{
		Title:             v["subTitle"],
		SupportURL:        v["subSupportUrl"],
		ProfileMode:       mode,
		ProfileURL:        v["subProfileUrl"],
		Announce:          v["subAnnounce"],
		EnableRouting:     b("subEnableRouting"),
		RoutingRules:      v["subRoutingRules"],
		HideSettings:      b("subHideSettings"),
		IncyEnableRouting: b("subIncyEnableRouting"),
		IncyRoutingRules:  v["subIncyRoutingRules"],
		Happ: HappConfig{
			AutoDetect:          b("subHappAutoDetect"),
			ProviderID:          v["subHappProviderId"],
			NewURL:              v["subHappNewUrl"],
			FallbackURL:         v["subHappFallbackUrl"],
			SubInfoColor:        v["subHappSubInfoColor"],
			SubInfoText:         v["subHappSubInfoText"],
			SubInfoButtonText:   v["subHappSubInfoButtonText"],
			SubInfoButtonLink:   v["subHappSubInfoButtonLink"],
			SubExpire:           b("subHappSubExpire"),
			SubExpireButtonLink: v["subHappSubExpireButtonLink"],
			NotificationExpire:  b("subHappNotificationExpire"),
			NoLimit:             b("subHappNoLimit"),
			AlwaysHwid:          b("subHappAlwaysHwid"),
			TunMode:             v["subHappTunMode"],
			TunType:             v["subHappTunType"],
			ExcludeRoutes:       v["subHappExcludeRoutes"],
			ExcludeApns:         b("subHappExcludeApns"),
			ColorProfile:        v["subHappColorProfile"],
			PingType:            v["subHappPingType"],
			AutoConnect:         b("subHappAutoConnect"),
			AutoConnectType:     v["subHappAutoConnectType"],
			PerAppMode:          v["subHappPerAppMode"],
			PerAppList:          v["subHappPerAppList"],
			LocalProxyAuth:      v["subHappLocalProxyAuth"],
		},
	}, nil
}

// subProfileURLKeys must be http(s) URLs (placeholders allowed).
var subProfileURLKeys = map[string]bool{
	"subSupportUrl": true, "subProfileUrl": true,
	"subHappNewUrl": true, "subHappFallbackUrl": true,
	"subHappSubInfoButtonLink": true, "subHappSubExpireButtonLink": true,
}

// subProfileMultiline values travel base64-encoded (Announce) or in the body
// (Incy), so a newline is fine there; everything else becomes a header value
// verbatim and must be a single line.
var subProfileMultiline = map[string]bool{"subAnnounce": true, "subRoutingRules": true, "subIncyRoutingRules": true, "subTitle": true}

var subProfileEnums = map[string][]string{
	"subProfileMode":         {SubProfileModeNone, SubProfileModeBuiltin, SubProfileModeCustom},
	"subHappSubInfoColor":    {"", "blue", "green", "red", "primary", "info", "success", "warning", "danger"},
	"subHappAutoConnectType": {"", "lowestdelay", "lastused", "fastest", "last"},
	"subHappPerAppMode":      {"", "off", "on", "bypass", "include", "exclude"},
	"subHappLocalProxyAuth":  {"", "auto", "on", "off"},
}

// ValidateSubProfileSetting checks one profile-header setting on Save, so a
// value that would break every subscription response (a CR/LF in a header) is
// refused at the form rather than discovered by clients.
func ValidateSubProfileSetting(key, value string) error {
	if _, ok := subProfileDefaults[key]; !ok {
		return nil
	}
	if value == "" {
		return nil
	}
	if allowed, ok := subProfileEnums[key]; ok {
		for _, a := range allowed {
			if strings.EqualFold(a, value) {
				return nil
			}
		}
		return common.NewErrorf("%s: unsupported value %q", key, value)
	}
	if !subProfileMultiline[key] && !httpguts.ValidHeaderFieldValue(value) {
		return common.NewErrorf("%s: the value contains a control character or line break", key)
	}
	if subProfileURLKeys[key] {
		u, err := url.Parse(strings.NewReplacer("{{", "", "}}", "").Replace(value))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return common.NewErrorf("%s: must be an http(s) URL", key)
		}
	}
	if key == "subRoutingRules" {
		if _, err := NormalizeHappRouting(value, true); err != nil && !errors.Is(err, errRoutingRemote) {
			return common.NewErrorf("%s: %v", key, err)
		}
	}
	if key == "subIncyRoutingRules" && strings.ContainsAny(value, "\r\n") {
		return common.NewErrorf("%s: must be a single line (an https URL or an incy:// deeplink)", key)
	}
	return nil
}
