package core

import (
	s3uiAnytls "github.com/samur005/3s-ui/core/protocol/anytls"
	s3uiHysteria "github.com/samur005/3s-ui/core/protocol/hysteria"
	s3uiHysteria2 "github.com/samur005/3s-ui/core/protocol/hysteria2"
	s3uiMieru "github.com/samur005/3s-ui/core/protocol/mieru"
	s3uiTrojan "github.com/samur005/3s-ui/core/protocol/trojan"
	s3uiTuic "github.com/samur005/3s-ui/core/protocol/tuic"
	s3uiVless "github.com/samur005/3s-ui/core/protocol/vless"
	s3uiVmess "github.com/samur005/3s-ui/core/protocol/vmess"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/protocol/shadowsocks"
	sbCommon "github.com/sagernet/sing/common"
)

func (c *Core) UpdateInboundUsers(config []byte) (bool, error) {
	box, err := c.running()
	if err != nil {
		return false, err
	}
	var inboundConfig option.Inbound
	if err = inboundConfig.UnmarshalJSONContext(box.ctx, config); err != nil {
		return false, err
	}
	inb, found := box.Inbound().Get(inboundConfig.Tag)
	if !found {
		return false, nil
	}
	switch options := inboundConfig.Options.(type) {
	case *option.VLESSInboundOptions:
		if in, ok := inb.(*s3uiVless.Inbound); ok {
			return true, in.UpdateUsers(options.Users)
		}
	case *option.VMessInboundOptions:
		if in, ok := inb.(*s3uiVmess.Inbound); ok {
			return true, in.UpdateUsers(options.Users)
		}
	case *option.TrojanInboundOptions:
		if in, ok := inb.(*s3uiTrojan.Inbound); ok {
			return true, in.UpdateUsers(options.Users)
		}
	case *option.TUICInboundOptions:
		if in, ok := inb.(*s3uiTuic.Inbound); ok {
			return true, in.UpdateUsers(options.Users)
		}
	case *option.HysteriaInboundOptions:
		if in, ok := inb.(*s3uiHysteria.Inbound); ok {
			return true, in.UpdateUsers(options.Users)
		}
	case *option.Hysteria2InboundOptions:
		if in, ok := inb.(*s3uiHysteria2.Inbound); ok {
			return true, in.UpdateUsers(options.Users)
		}
	case *option.AnyTLSInboundOptions:
		if in, ok := inb.(*s3uiAnytls.Inbound); ok {
			return true, in.UpdateUsers(options.Users)
		}
	case *s3uiMieru.InboundOptions:
		if in, ok := inb.(*s3uiMieru.Inbound); ok {
			return true, in.UpdateUsers(options.Users)
		}
	case *option.ShadowsocksInboundOptions:
		if options.Managed || len(options.Users) == 0 {
			return false, nil
		}
		if in, ok := inb.(*shadowsocks.MultiInbound); ok {
			return true, in.UpdateUsers(sbCommon.Map(options.Users, func(it option.ShadowsocksUser) string {
				return it.Name
			}), sbCommon.Map(options.Users, func(it option.ShadowsocksUser) string {
				return it.Password
			}))
		}
	}
	return false, nil
}
