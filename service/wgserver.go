package service

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/samur005/3s-ui/database"
	"github.com/samur005/3s-ui/database/model"
	"github.com/samur005/3s-ui/logger"
	"github.com/samur005/3s-ui/qwdtt"
	"github.com/samur005/3s-ui/util/common"
	"github.com/samur005/3s-ui/wgserver"
	"gorm.io/gorm"
)

func init() {
	for _, t := range wgserver.Types() {
		typ := t
		registerExtDriver(&extDriver{
			Type:   typ,
			Engine: EngineExternal,
			Normalize: func(tx *gorm.DB, id uint, options, stored json.RawMessage) (json.RawMessage, error) {
				return wgserver.NormalizeOptions(tx, typ, id, options, stored)
			},
			OnDelete: func(tx *gorm.DB, id uint) error { return wgserver.DeletePeers(tx, id) },
			Apply:    func() { (&WgService{}).ApplyStored() },
		})
	}
}

// WgService is the panel-side glue around the wgserver package: it applies
// saved inbounds to the running devices and the core, and renders what clients
// are handed.
type WgService struct {
	SettingService
}

// ApplyStored makes the stored WireGuard/AmneziaWG inbounds take effect after
// an inbounds save, switch or delete committed: the core is restarted when the
// TUN inbounds it needs changed, and the devices and routing are reconciled.
func (s *WgService) ApplyStored() {
	var cs ConfigService
	if wgserver.CoreNeedsRestart() && cs.CoreRunning() {
		cs.requestRestart()
	}
	wgserver.Get().Reconcile()
}

// resolveHost is the server address to put in client configs when the inbound
// names none: the subscription domain, the panel domain, or the host's
// outbound IPv4 address.
func (s *WgService) resolveHost() string {
	for _, get := range []func() (string, error){s.SettingService.GetSubDomain, s.SettingService.GetWebDomain} {
		if h, err := get(); err == nil && strings.TrimSpace(h) != "" {
			return strings.TrimSpace(h)
		}
	}
	return qwdtt.DetectOutboundIPv4()
}

// WgClientEntry is a client's config on one inbound.
type WgClientEntry struct {
	InboundID uint   `json:"inboundId"`
	Tag       string `json:"tag"`
	Type      string `json:"type"`
	Version   string `json:"version,omitempty"`
	Enable    bool   `json:"enable"`
	wgserver.ClientConfig
}

// ClientConfigs renders the configs of one client on every WireGuard and
// AmneziaWG inbound it is bound to. Disabled inbounds are included (the panel
// shows them) but flagged; the subscription leaves them out.
func (s *WgService) ClientConfigs(clientID uint) ([]WgClientEntry, error) {
	db := database.GetDB()
	var client model.Client
	if err := db.Select("id", "name").Where("id = ?", clientID).First(&client).Error; err != nil {
		return nil, common.NewError("client not found")
	}
	inbounds, err := wgserver.LoadInbounds(db)
	if err != nil {
		return nil, err
	}
	host := ""
	out := []WgClientEntry{}
	for _, in := range inbounds {
		peer, ok, err := wgserver.PeerFor(db, in, clientID)
		var full *wgserver.PoolFullError
		if errors.As(err, &full) {
			logger.Warning("wireguard: ", err)
			continue
		}
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if host == "" && in.Config.Endpoint == "" {
			host = s.resolveHost()
		}
		cc, err := wgserver.BuildClient(in.Type, in.Config, peer, host, client.Name+" · "+in.Tag)
		if err != nil {
			return nil, common.NewError(in.Tag + ": " + err.Error())
		}
		out = append(out, WgClientEntry{InboundID: in.ID, Tag: in.Tag, Type: in.Type, Version: in.Config.Version, Enable: in.Enable, ClientConfig: cc})
	}
	return out, nil
}

// ClientLinks are the share links for a client's subscription: wireguard://
// for WireGuard inbounds and vpn:// (the Amnezia app's link) for AmneziaWG
// ones. Only enabled inbounds count. A problem with one inbound is logged and
// skips it, so it cannot take the whole subscription down.
func (s *WgService) ClientLinks(client *model.Client) []string {
	if client == nil || client.Id == 0 {
		return nil
	}
	entries, err := s.ClientConfigs(client.Id)
	if err != nil {
		logger.Warning("wireguard: client links: ", err)
		return nil
	}
	var links []string
	for _, e := range entries {
		if !e.Enable {
			continue
		}
		switch {
		case e.URI != "":
			links = append(links, e.URI)
		case e.Vpn != "":
			links = append(links, e.Vpn)
		}
	}
	return links
}

// ClientConfFiles returns "name -> .conf" for a client, optionally one inbound.
func (s *WgService) ClientConfFiles(clientID uint, inboundID uint) ([]WgClientEntry, error) {
	entries, err := s.ClientConfigs(clientID)
	if err != nil {
		return nil, err
	}
	out := entries[:0]
	for _, e := range entries {
		if !e.Enable || (inboundID != 0 && e.InboundID != inboundID) {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// WgStatus is the live state of one inbound for the form.
type WgStatus struct {
	wgserver.Status
	// PublicKey is the server's public key, as shown on the form.
	PublicKey string `json:"publicKey"`
}

// Status returns the live state of one WireGuard/AmneziaWG inbound.
func (s *WgService) Status(inboundID uint) (*WgStatus, error) {
	in, err := wgserver.LoadInbound(database.GetDB(), inboundID)
	if err != nil {
		return nil, err
	}
	if in == nil {
		return nil, common.NewError("not a WireGuard or AmneziaWG inbound")
	}
	return &WgStatus{Status: wgserver.Get().Status(*in), PublicKey: in.Config.PublicKey}, nil
}

// GenerateObfuscation returns a fresh random AmneziaWG parameter set for a
// version, for the form's generate button.
func (s *WgService) GenerateObfuscation(version string) (any, error) {
	if !wgserver.ValidVersion(version) {
		return nil, common.NewErrorf("unknown AmneziaWG version %q", version)
	}
	return struct {
		wgserver.Obfuscation
		Fields wgserver.FieldSet `json:"fields"`
	}{wgserver.Generate(version), wgserver.Fields(version)}, nil
}
