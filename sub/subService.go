package sub

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
	"github.com/samur005/3s-ui/service"
	"github.com/samur005/3s-ui/util"
)

type SubService struct {
	service.SettingService
	LinkService
	qwdtt service.QwdttService
	wg    service.WgService
	xray  service.XrayService
}

// withQwdtt appends the client's qwdtt:// link, when qWDTT is on and the client
// is bound to the qWDTT inbound. The link comes from the live qWDTT configuration rather
// than from client.Links: its VK hashes are replaced as calls end, and every
// subscription has to hand out the current ones.
func (s *SubService) withQwdtt(client *model.Client, links []string) []string {
	if link := s.qwdtt.ClientLink(client); link != "" {
		return append(links, link)
	}
	return links
}

// withWireguard appends the client's WireGuard (wireguard://) and AmneziaWG
// (Amnezia vpn://) links, one per inbound the client is bound to.
func (s *SubService) withWireguard(client *model.Client, links []string) []string {
	return append(links, s.wg.ClientLinks(client)...)
}

// withXray appends vless:// links for every Xray VLESS inbound the client is bound to.
func (s *SubService) withXray(client *model.Client, links []string) []string {
	return append(links, s.xray.LinksForClient(client)...)
}

func (s *SubService) GetSubs(subId string) (*string, []string, error) {
	return s.getSubs(subId, nil)
}

// getSubs is the plain link subscription, its links passed through transform
// first when one is given (happLinks, for Happ).
func (s *SubService) getSubs(subId string, transform func([]string) []string) (*string, []string, error) {
	client, err := s.getClientBySubId(subId)
	if err != nil {
		return nil, nil, err
	}

	clientInfo := ""
	subShowInfo, _ := s.SettingService.GetSubShowInfo()
	if subShowInfo {
		clientInfo = s.getClientInfo(client)
	}

	linksArray := s.LinkService.GetLinks(&client.Links, "all", clientInfo)
	if transform != nil {
		linksArray = transform(linksArray)
	}
	linksArray = s.withQwdtt(client, linksArray)
	linksArray = s.withWireguard(client, linksArray)
	linksArray = s.withXray(client, linksArray)
	// Incy reads its routing profile from a line in the body.
	if cfg, err := s.SettingService.GetSubProfileConfig(); err == nil && cfg.IncyEnableRouting {
		if line := service.IncyRoutingLine(cfg.IncyRoutingRules); line != "" {
			linksArray = append(linksArray, line)
		}
	}
	result := strings.Join(linksArray, "\n")

	headers := s.getClientHeaders(client)

	subEncode, _ := s.SettingService.GetSubEncode()
	if subEncode {
		result = base64.StdEncoding.EncodeToString([]byte(result))
	}

	return &result, headers, nil
}

// ClientInfo is the payload served by the subscriber dashboard's JSON info
// endpoint. It carries everything the Vue page needs to render live status:
// expiry/remaining days, used vs total traffic, and the config links.
type ClientInfo struct {
	Name             string `json:"name"`
	Remark           string `json:"remark,omitempty"`
	Title            string `json:"title"`
	Enable           bool   `json:"enable"`
	Expiry           int64  `json:"expiry"`
	RemainingDays    int64  `json:"remainingDays"`
	Volume           int64  `json:"volume"`
	Up               int64  `json:"up"`
	Down             int64  `json:"down"`
	Used             int64  `json:"used"`
	RemainingTraffic int64  `json:"remainingTraffic"`
	Unlimited        bool   `json:"unlimited"`
	// Expired is decided here rather than from Expiry in the browser, so the
	// page cannot disagree with the panel over a skewed client clock.
	Expired bool     `json:"expired"`
	Links   []string `json:"links"`
}

// getClientInfoJSON resolves the given sub id and returns the live info the
// subscriber dashboard displays. Links are the plain URIs (no injected traffic
// info), matching what a proxy client would consume from the raw subscribe.
func (s *SubService) getClientInfoJSON(subId string) (*ClientInfo, error) {
	client, err := s.getAnyClientBySubId(subId)
	if err != nil {
		return nil, err
	}

	now := time.Now().Unix()

	// A client that is off gets its state and nothing else. Disabling one is
	// how a leaked subscription URL is revoked, and a label, a quota, an
	// expiry date and a set of counters would keep answering that URL for as
	// long as the client row exists -- so everything but the state stays
	// behind the enable flag. Links most of all: the page has to explain a
	// dead subscription, not hand out working configs for one. Name is echoed
	// straight back from the URL, so it says nothing the caller did not
	// already type. serveDashboard withholds the matching profile headers.
	info := &ClientInfo{
		Name:   client.Name,
		Title:  client.Name,
		Enable: client.Enable,
		// Never null: the page's own type says string[].
		Links: []string{},
	}
	if client.Expiry > 0 {
		info.Expired = client.Expiry <= now
	}
	if !client.Enable {
		return info, nil
	}

	info.Remark = client.Remark
	info.Expiry = client.Expiry
	info.Volume = client.Volume
	info.Up = client.Up
	info.Down = client.Down
	info.Used = client.Up + client.Down
	info.Unlimited = client.Volume <= 0
	if client.Remark != "" {
		info.Title = client.Remark
	}

	info.RemainingTraffic = client.Volume - info.Used
	if info.RemainingTraffic < 0 {
		info.RemainingTraffic = 0
	}

	if client.Expiry > 0 {
		info.RemainingDays = (client.Expiry - now) / 86400
		if info.RemainingDays < 0 {
			info.RemainingDays = 0
		}
	}

	info.Links = append(info.Links, s.LinkService.GetLinks(&client.Links, "all", "")...)
	info.Links = s.withQwdtt(client, info.Links)
	info.Links = s.withWireguard(client, info.Links)
	info.Links = s.withXray(client, info.Links)

	return info, nil
}

// getAnyClientBySubId resolves a sub id without getClientBySubId's enable
// filter. Only the subscriber dashboard uses it: a client that has been
// disabled asking why its subscription stopped working is precisely what that
// page exists for, and the filter turns that into a bare "Error!". Everything
// that hands out a config keeps the filter.
func (j *SubService) getAnyClientBySubId(subId string) (*model.Client, error) {
	db := database.GetDB()
	client := &model.Client{}
	err := db.Model(model.Client{}).Where("name = ?", subId).First(client).Error
	if err != nil {
		return nil, err
	}
	return client, nil
}

func (j *SubService) getClientBySubId(subId string) (*model.Client, error) {
	db := database.GetDB()
	client := &model.Client{}
	err := db.Model(model.Client{}).Where("enable = true and name = ?", subId).First(client).Error
	if err != nil {
		return nil, err
	}
	// Node exclusion and duplicate removal (service/nodeSubscription.go), on
	// the in-memory copy only.
	client.Links = service.FilterNodeLinks(client.Links)
	return client, nil
}

func (s *SubService) getClientHeaders(client *model.Client) []string {
	updateInterval, _ := s.SettingService.GetSubUpdates()
	return util.GetHeaders(client, updateInterval)
}

func (s *SubService) getClientInfo(c *model.Client) string {
	now := time.Now().Unix()

	var result []string
	if vol := c.Volume - (c.Up + c.Down); vol > 0 {
		result = append(result, fmt.Sprintf("%s%s", s.formatTraffic(vol), "📊"))
	}
	if c.Expiry > 0 {
		result = append(result, fmt.Sprintf("%d%s⏳", (c.Expiry-now)/86400, "Days"))
	}
	if len(result) > 0 {
		return " " + strings.Join(result, " ")
	} else {
		return " ♾"
	}
}

func (s *SubService) formatTraffic(trafficBytes int64) string {
	if trafficBytes < 1024 {
		return fmt.Sprintf("%.2fB", float64(trafficBytes)/float64(1))
	} else if trafficBytes < (1024 * 1024) {
		return fmt.Sprintf("%.2fKB", float64(trafficBytes)/float64(1024))
	} else if trafficBytes < (1024 * 1024 * 1024) {
		return fmt.Sprintf("%.2fMB", float64(trafficBytes)/float64(1024*1024))
	} else if trafficBytes < (1024 * 1024 * 1024 * 1024) {
		return fmt.Sprintf("%.2fGB", float64(trafficBytes)/float64(1024*1024*1024))
	} else if trafficBytes < (1024 * 1024 * 1024 * 1024 * 1024) {
		return fmt.Sprintf("%.2fTB", float64(trafficBytes)/float64(1024*1024*1024*1024))
	} else {
		return fmt.Sprintf("%.2fEB", float64(trafficBytes)/float64(1024*1024*1024*1024*1024))
	}
}
