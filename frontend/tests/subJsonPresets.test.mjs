// Run with: node tests/subJsonPresets.test.mjs (Node 22.18+ strips the types itself).
import { dropRemovedPresets, geo, geoList, geositeList } from '../src/plugins/subJsonPresets.ts'
import assert from 'node:assert'
const ext = {
  rule_set: [{tag:'geosite-ir',type:'remote'},{tag:'geoip-cn'},{tag:'geosite-private'},{tag:'mine'}],
  rules: [
    {action:'sniff'},
    {rule_set:['geosite-ir','geoip-ir','geosite-private'],action:'route',outbound:'direct'},
    {rule_set:['geosite-cn','geosite-vn'],action:'reject'},
  ],
  dns: {servers:[], rules:[{rule_set:['geosite-ir'],action:'route',server:'direct-dns'}, {rule_set:['geosite-ads'],action:'route',server:'direct-dns'}]},
}
assert.equal(dropRemovedPresets(ext), true)
assert.deepEqual(ext.rule_set.map(r=>r.tag), ['geosite-private','mine'])
assert.deepEqual(ext.rules, [{action:'sniff'},{rule_set:['geosite-private'],action:'route',outbound:'direct'}])
assert.deepEqual(ext.dns.rules.map(r=>r.rule_set), [['geosite-ads']])
assert.equal(dropRemovedPresets(ext), false)
const only = {rule_set:[{tag:'geoip-vn'}], rules:[{rule_set:['geoip-vn'],action:'reject'}]}
dropRemovedPresets(only); assert.deepEqual(only, {})
for (const bad of [null, 5, 'x', [], {rule_set:'weird', rules:'weird', dns: 3}]) dropRemovedPresets(bad)
for (const l of [...geoList, ...geositeList]) assert.ok(geo.some(g=>g.tag===l.value), l.value)
assert.ok(!geoList.some(x=>/Iran|China|Vietnam/.test(x.title)))
assert.ok(geoList[0].title.includes('🇷🇺'))
console.log('ok')
