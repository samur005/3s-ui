// Rule-set presets of Settings -> JSON subscription: the lists the "route to
// Direct" / "route to Block" / "DNS to direct" pickers offer and the remote
// rule-set each one pulls in.
//
// Russian sets come first: the Russian geo-sets are the RoscomVPN ones
// (hydraponique/roscomvpn-geosite and -geoip) plus the Russian IP set of
// runetfreedom/russia-v2ray-rules-dat, all served through jsDelivr. The
// Iranian, Chinese and Vietnamese sets were taken out; settings saved with them
// are cleaned by dropRemovedPresets.

export interface RuleSetPreset {
  tag: string
  type: 'remote'
  format: 'binary'
  url: string
  download_detour: string
}

const CDN = 'https://testingcf.jsdelivr.net/gh/'
const META = CDN + 'MetaCubeX/meta-rules-dat@sing/geo/'
const ROSCOM_SITE = CDN + 'hydraponique/roscomvpn-geosite@release/sing-box/'
const ROSCOM_IP = CDN + 'hydraponique/roscomvpn-geoip@release/sing-box/'
const RUNET = CDN + 'runetfreedom/russia-v2ray-rules-dat@release/sing-box/'

const remote = (tag: string, url: string): RuleSetPreset => ({
  tag, type: 'remote', format: 'binary', url, download_detour: 'direct',
})

// The catalog every picker draws from.
export const geo: RuleSetPreset[] = [
  remote('geosite-ru', ROSCOM_SITE + 'category-ru.srs'),
  remote('geoip-ru', RUNET + 'rule-set-geoip/geoip-ru.srs'),
  remote('geosite-ru-whitelist', ROSCOM_SITE + 'whitelist.srs'),
  remote('geoip-ru-whitelist', ROSCOM_IP + 'whitelist.srs'),
  remote('geosite-private', META + 'geosite/private.srs'),
  remote('geoip-private', META + 'geoip/private.srs'),
  remote('geosite-ads', META + 'geosite/category-ads-all.srs'),
]

// Domain sets only: the DNS "to direct" rule matches names.
export const geositeList = [
  { title: '🇷🇺 Russia', value: 'geosite-ru' },
  { title: '🇷🇺 Whitelist', value: 'geosite-ru-whitelist' },
  { title: 'Private', value: 'geosite-private' },
  { title: 'Ads', value: 'geosite-ads' },
]

// Domain and IP sets for the routing rules.
export const geoList = [
  { title: '🇷🇺 Site-Russia', value: 'geosite-ru' },
  { title: '🇷🇺 IP-Russia', value: 'geoip-ru' },
  { title: '🇷🇺 Site-Whitelist', value: 'geosite-ru-whitelist' },
  { title: '🇷🇺 IP-Whitelist', value: 'geoip-ru-whitelist' },
  { title: 'Site-Private', value: 'geosite-private' },
  { title: 'IP-Private', value: 'geoip-private' },
  { title: 'Site-Ads', value: 'geosite-ads' },
]

// Presets that earlier versions offered and that no longer exist.
export const removedPresetTags: string[] = [
  'geosite-ir', 'geoip-ir', 'geosite-cn', 'geoip-cn', 'geosite-vn', 'geoip-vn',
]

const isObject = (v: unknown): v is Record<string, any> => typeof v === 'object' && v !== null && !Array.isArray(v)

// Drops a removed preset's tag from one rule list: the tag leaves each rule's
// rule_set, and a rule left with no rule_set (and nothing else that could make
// it match) goes with it. Anything that is not an array is left alone, as the
// JSON editor takes any value. Returns whether anything changed.
const cleanRules = (rules: unknown): boolean => {
  if (!Array.isArray(rules)) return false
  let changed = false
  for (let i = rules.length - 1; i >= 0; i--) {
    const rule = rules[i]
    if (!isObject(rule) || !Array.isArray(rule.rule_set)) continue
    const kept = rule.rule_set.filter((t: unknown) => !removedPresetTags.includes(t as string))
    if (kept.length === rule.rule_set.length) continue
    changed = true
    if (kept.length > 0) {
      rule.rule_set = kept
    } else {
      rules.splice(i, 1)
    }
  }
  return changed
}

// Removes every trace of the retired presets from a parsed subJsonExt object,
// in place: their rule-set definitions and their tags in the routing and DNS
// rules. The rest is untouched. Returns true when something was removed.
export function dropRemovedPresets(ext: unknown): boolean {
  if (!isObject(ext)) return false
  let changed = false
  if (Array.isArray(ext.rule_set)) {
    const kept = ext.rule_set.filter((rs: any) => !(isObject(rs) && removedPresetTags.includes(rs.tag)))
    if (kept.length !== ext.rule_set.length) {
      changed = true
      if (kept.length > 0) ext.rule_set = kept
      else delete ext.rule_set
    }
  }
  if (cleanRules(ext.rules)) {
    changed = true
    if (Array.isArray(ext.rules) && ext.rules.length === 0) delete ext.rules
  }
  if (isObject(ext.dns) && cleanRules(ext.dns.rules)) changed = true
  return changed
}
