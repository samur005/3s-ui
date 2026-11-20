import { InTypes } from '@/types/inbounds'
import type { IconName } from '@/components/ui/icons'

/** Map inbound type → icon name in the shared stroke set. */
const MAP: Record<string, IconName> = {
  [InTypes.VLESS]: 'protoVless',
  [InTypes.VMess]: 'protoVmess',
  [InTypes.Trojan]: 'protoTrojan',
  [InTypes.Shadowsocks]: 'protoShadowsocks',
  [InTypes.ShadowTLS]: 'protoShadowtls',
  [InTypes.Hysteria]: 'protoHysteria',
  [InTypes.Hysteria2]: 'protoHysteria2',
  [InTypes.TUIC]: 'protoTuic',
  [InTypes.Naive]: 'protoNaive',
  [InTypes.AnyTls]: 'protoAnytls',
  [InTypes.Mieru]: 'protoMieru',
  [InTypes.Snell]: 'protoSnell',
  [InTypes.Mixed]: 'protoMixed',
  [InTypes.SOCKS]: 'protoSocks',
  [InTypes.HTTP]: 'protoHttp',
  [InTypes.Direct]: 'protoDirect',
  [InTypes.Tun]: 'protoTun',
  [InTypes.Redirect]: 'protoRedirect',
  [InTypes.TProxy]: 'protoTproxy',
  [InTypes.Cloudflared]: 'protoCloudflared',
  [InTypes.Xvless]: 'protoXvless',
  [InTypes.Qwdtt]: 'protoQwdtt',
  [InTypes.Wg]: 'protoWg',
  [InTypes.Awg]: 'protoAwg',
}

/** Icon for an inbound protocol type; unknown types use the generic inbound glyph. */
export function protoIcon(type: string): IconName {
  return MAP[type] ?? 'inbound'
}

/** Every InTypes value that must have a dedicated glyph (for tests). */
export const PROTO_ICON_TYPES: string[] = Object.values(InTypes)

export function missingProtoIcons(): string[] {
  return PROTO_ICON_TYPES.filter((t) => !MAP[t])
}
