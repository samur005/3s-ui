#!/usr/bin/env node
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = join(dirname(fileURLToPath(import.meta.url)), '..')
const icons = readFileSync(join(root, 'src/components/ui/icons.ts'), 'utf8')
const proto = readFileSync(join(root, 'src/plugins/protoIcon.ts'), 'utf8')
const inbounds = readFileSync(join(root, 'src/types/inbounds.ts'), 'utf8')

const block = inbounds.match(/export const InTypes = \{([\s\S]*?)\n\}/)
if (!block) {
  console.error('InTypes block not found')
  process.exit(1)
}
const inTypeKeys = [...block[1].matchAll(/^\s+(\w+):\s+'/gm)].map((m) => m[1])
const mapped = [...proto.matchAll(/InTypes\.(\w+)\]:\s*'(\w+)'/g)].map((m) => [m[1], m[2]])
const mappedKeys = mapped.map((m) => m[0])
const missingMap = inTypeKeys.filter((k) => !mappedKeys.includes(k))
const missingGlyph = mapped.map((m) => m[1]).filter((name) => name !== 'inbound' && !new RegExp(`\\b${name}:`).test(icons))

if (missingMap.length || missingGlyph.length) {
  console.error({ missingMap, missingGlyph, inTypeKeys, mappedKeys })
  process.exit(1)
}
console.log(`ok: ${mapped.length} protocol icons for InTypes (${inTypeKeys.join(',')})`)
