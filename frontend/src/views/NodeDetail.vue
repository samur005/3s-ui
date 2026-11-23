<template>
  <div class="page-stack fade-up">
    <div class="toolbar" style="gap: 8px; flex-wrap: wrap;">
      <Btn sm @click="$router.push('/nodes')"><Ico name="chevron" :size="14" style="transform: rotate(180deg);" /> {{ $t('pages.nodes') }}</Btn>
      <div style="flex: 1; min-width: 0; font-weight: 700; font-size: 15px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis;">
        {{ detail?.node?.name ?? '#' + nodeId }}
        <span v-if="detail?.node" class="mono" style="font-weight: 500; font-size: 12px; color: var(--text-3);">{{ detail.node.baseUrl }}</span>
      </div>
      <Chip v-if="health" :color="healthColor">{{ healthText }}</Chip>
      <Chip v-if="detail?.legacy" color="amber" :title="$t('node.detail.legacyHint')">API 1</Chip>
      <Chip v-else-if="detail?.status?.apiVersion">API {{ detail.status.apiVersion }}</Chip>
      <Btn sm :loading="loadingDetail" @click="loadDetail"><Ico name="refresh" :size="14" /></Btn>
    </div>

    <div v-if="loadingDetail && !detail" class="card" style="padding: 24px; text-align: center; color: var(--text-3);">{{ $t('loading') }}</div>
    <EmptyState v-else-if="detailError && !detail" icon="server" :title="$t('failed')" :desc="detailError" />

    <template v-else-if="detail">
      <Tabs v-model="tab" :tabs="tabList" page />

      <!-- ============ Overview ============ -->
      <div v-if="tab === 'overview'" class="card" style="padding: 16px; display: flex; flex-direction: column; gap: 10px;">
        <div v-if="detail.status.reasons?.length" style="color: var(--amber); font-size: 13px;">
          <div v-for="(r, i) in detail.status.reasons" :key="i">• {{ r }}</div>
        </div>
        <div v-for="row in overviewRows" :key="row.k" class="kv-row">
          <span class="k">{{ row.k }}</span><span class="v" :class="{ mono: row.mono }">{{ row.v }}</span>
        </div>
        <hr class="form-divider" />
        <div style="display: flex; gap: 8px; flex-wrap: wrap;">
          <Btn sm :loading="busy === 'reconnect'" @click="reconnect"><Ico name="refresh" :size="14" /> {{ $t('node.detail.reconnect') }}</Btn>
          <Btn sm :loading="busy === 'reconcile'" @click="reconcileNow"><Ico name="refresh" :size="14" /> {{ $t('node.reconcile') }}</Btn>
          <Btn sm :loading="busy === 'core.restart'" @click="ask('core.restart')"><Ico name="bolt" :size="14" /> {{ $t('node.detail.coreRestart') }}</Btn>
          <Btn v-if="!detail.status.maintenance" sm :loading="busy === 'core.stop'" @click="ask('core.stop')"><Ico name="pause" :size="14" /> {{ $t('node.detail.coreStop') }}</Btn>
          <Btn v-else sm :loading="busy === 'core.start'" @click="ask('core.start')"><Ico name="bolt" :size="14" /> {{ $t('node.detail.coreStart') }}</Btn>
          <Btn sm :loading="busy === 'panel.restart'" style="color: var(--rose);" @click="ask('panel.restart')"><Ico name="refresh" :size="14" /> {{ $t('node.detail.panelRestart') }}</Btn>
        </div>
      </div>

      <!-- ============ object lists: inbounds / outbounds / endpoints ============ -->
      <div v-else-if="isObjectTab" class="card" style="padding: 16px; display: flex; flex-direction: column; gap: 10px;">
        <div style="display: flex; gap: 8px; align-items: center;">
          <div style="flex: 1; font-weight: 700;">{{ tabLabel(tab) }}</div>
          <select v-if="tab === 'outbounds'" class="input" style="width: auto;" v-model="outboundKind">
            <option value="outbounds">{{ $t('pages.outbounds') }}</option>
            <option value="endpoints">{{ $t('pages.endpoints') }}</option>
          </select>
          <Btn sm variant="primary" :disabled="!canWrite" @click="newObject"><Ico name="plus" :size="14" /> {{ $t('actions.add') }}</Btn>
          <Btn sm :loading="remoteLoading" @click="loadTab"><Ico name="refresh" :size="14" /></Btn>
        </div>
        <div v-if="remoteLoading && objects.length === 0" style="color: var(--text-3);">{{ $t('loading') }}</div>
        <div v-else-if="remoteError" style="color: var(--rose);">{{ remoteError }}</div>
        <div v-else-if="objects.length === 0" style="color: var(--text-3);">{{ $t('node.detail.empty') }}</div>
        <table v-else class="mn-table">
          <thead><tr><th>{{ $t('node.detail.tag') }}</th><th>{{ $t('type') }}</th><th>{{ $t('node.detail.port') }}</th><th></th></tr></thead>
          <tbody>
            <tr v-for="o in objects" :key="o.tag">
              <td class="mono">{{ o.tag }} <Chip v-if="isAdopted(o.tag)" color="brand">{{ $t('node.adopted') }}</Chip></td>
              <td>{{ o.type }}</td>
              <td class="mono">{{ o.listen_port ?? o.server_port ?? '' }}</td>
              <td style="text-align: end; white-space: nowrap;">
                <Btn sm v-if="writeObject === 'outbounds'" :loading="checking === o.tag" @click="checkOutbound(o.tag)">{{ $t('node.detail.check') }}</Btn>
                <Btn sm :disabled="!canWrite" @click="editObject(o)"><Ico name="edit" :size="14" /></Btn>
                <Btn sm :disabled="!canWrite || isAdopted(o.tag)" :title="isAdopted(o.tag) ? $t('node.detail.adoptedNoDelete') : ''" @click="askDelete(o.tag)"><Ico name="trash" :size="14" /></Btn>
              </td>
            </tr>
          </tbody>
        </table>
        <div v-if="checkResult" class="mono" style="font-size: 12px;">{{ checkResult }}</div>
      </div>

      <!-- ============ Clients (read-only: clients are managed on the master and synced) ============ -->
      <div v-else-if="tab === 'clients'" class="card" style="padding: 16px; display: flex; flex-direction: column; gap: 10px;">
        <MHint>{{ $t('node.detail.clientsHint') }}</MHint>
        <div v-if="remoteLoading" style="color: var(--text-3);">{{ $t('loading') }}</div>
        <div v-else-if="remoteError" style="color: var(--rose);">{{ remoteError }}</div>
        <div v-else-if="objects.length === 0" style="color: var(--text-3);">{{ $t('node.detail.empty') }}</div>
        <table v-else class="mn-table">
          <thead><tr><th>{{ $t('node.name') }}</th><th>{{ $t('enable') }}</th><th>{{ $t('node.detail.group') }}</th><th>↑ / ↓</th></tr></thead>
          <tbody>
            <tr v-for="c in objects" :key="c.id ?? c.name">
              <td>{{ c.name }}</td>
              <td>{{ c.enable ? '✓' : '—' }}</td>
              <td class="mono">{{ c.group ?? '' }}</td>
              <td class="mono">{{ bytes(c.up) }} / {{ bytes(c.down) }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <!-- ============ Routing / DNS ============ -->
      <div v-else-if="tab === 'routing' || tab === 'dns'" class="card" style="padding: 16px; display: flex; flex-direction: column; gap: 10px;">
        <MHint>{{ $t('node.detail.sectionHint') }}</MHint>
        <div v-if="remoteLoading" style="color: var(--text-3);">{{ $t('loading') }}</div>
        <div v-else-if="remoteError" style="color: var(--rose);">{{ remoteError }}</div>
        <template v-else>
          <textarea class="input mono" rows="18" v-model="sectionText" spellcheck="false" />
          <div v-if="sectionInvalid" style="color: var(--rose); font-size: 12.5px;">{{ $t('node.detail.invalidJson') }}</div>
          <div style="display: flex; gap: 8px;">
            <Btn sm :disabled="sectionInvalid || detail.legacy" :loading="busy === 'checkConfig'" @click="checkSection">{{ $t('node.detail.check') }}</Btn>
            <Btn sm variant="primary" :disabled="sectionInvalid || !canWrite" :loading="busy === 'saveSection'" @click="ask(tab === 'dns' ? 'config.dns' : 'config.route')">{{ $t('actions.save') }}</Btn>
          </div>
        </template>
      </div>

      <!-- ============ Capabilities ============ -->
      <div v-else-if="tab === 'capabilities'" class="card" style="padding: 16px; display: flex; flex-direction: column; gap: 12px;">
        <div v-if="detail.legacy" style="color: var(--amber);">{{ $t('node.detail.legacyHint') }}</div>
        <div v-else-if="!caps" style="color: var(--text-3);">{{ detail.capsError || $t('node.detail.capsUnknown') }}</div>
        <template v-else>
          <div class="kv-row"><span class="k">OS / Arch</span><span class="v mono">{{ caps.os }} / {{ caps.arch }} · {{ caps.goVersion }}</span></div>
          <div class="kv-row"><span class="k">API</span><span class="v mono">{{ caps.apiVersion }} (min master {{ caps.minMasterApi }})</span></div>
          <div v-for="g in capGroups" :key="g.k">
            <div style="font-size: 12px; font-weight: 700; color: var(--text-3); margin-bottom: 4px;">{{ g.k }}</div>
            <div style="display: flex; gap: 5px; flex-wrap: wrap;">
              <Chip v-for="v in g.v" :key="v">{{ v }}</Chip>
              <span v-if="g.v.length === 0" style="color: var(--text-3);">—</span>
            </div>
          </div>
        </template>
        <div><Btn sm :loading="busy === 'capabilities'" @click="refreshCaps"><Ico name="refresh" :size="14" /> {{ $t('node.detail.refreshCaps') }}</Btn></div>
      </div>

      <!-- ============ Traffic ============ -->
      <div v-else-if="tab === 'traffic'" class="card" style="padding: 16px; display: flex; flex-direction: column; gap: 8px;">
        <div v-if="!detail.status.net" style="color: var(--text-3);">{{ $t('node.detail.empty') }}</div>
        <template v-else>
          <div class="kv-row"><span class="k">↑ {{ $t('node.detail.rate') }}</span><span class="v mono">{{ bytes(detail.status.net.up) }}/s</span></div>
          <div class="kv-row"><span class="k">↓ {{ $t('node.detail.rate') }}</span><span class="v mono">{{ bytes(detail.status.net.down) }}/s</span></div>
          <div class="kv-row"><span class="k">↑ {{ $t('node.detail.total') }}</span><span class="v mono">{{ bytes(detail.status.net.sent) }}</span></div>
          <div class="kv-row"><span class="k">↓ {{ $t('node.detail.total') }}</span><span class="v mono">{{ bytes(detail.status.net.recv) }}</span></div>
        </template>
        <MHint>{{ $t('node.detail.trafficHint') }}</MHint>
      </div>

      <!-- ============ Services ============ -->
      <div v-else-if="tab === 'services'" class="card" style="padding: 16px; display: flex; flex-direction: column; gap: 10px;">
        <div v-if="remoteLoading" style="color: var(--text-3);">{{ $t('loading') }}</div>
        <div v-else-if="remoteError" style="color: var(--rose);">{{ remoteError }}</div>
        <template v-else>
          <div v-for="(v, k) in servicesInfo" :key="k" class="kv-row">
            <span class="k">{{ k }}</span><span class="v mono">{{ typeof v === 'object' ? JSON.stringify(v) : v }}</span>
          </div>
          <div v-if="Object.keys(servicesInfo).length === 0" style="color: var(--text-3);">{{ detail.legacy ? $t('node.detail.legacyHint') : $t('node.detail.empty') }}</div>
        </template>
      </div>

      <!-- ============ Logs ============ -->
      <div v-else-if="tab === 'logs'" class="card" style="padding: 16px; display: flex; flex-direction: column; gap: 10px;">
        <div style="display: flex; gap: 8px;">
          <select class="input" style="width: auto;" v-model="logLevel">
            <option v-for="l in ['debug', 'info', 'warning', 'error']" :key="l" :value="l">{{ l }}</option>
          </select>
          <select class="input" style="width: auto;" v-model="logCount">
            <option v-for="c in [50, 100, 200, 500]" :key="c" :value="c">{{ c }}</option>
          </select>
          <Btn sm :loading="remoteLoading" @click="loadTab"><Ico name="refresh" :size="14" /></Btn>
        </div>
        <div v-if="remoteError" style="color: var(--rose);">{{ remoteError }}</div>
        <pre v-else class="mono" style="font-size: 11.5px; white-space: pre-wrap; max-height: 60vh; overflow: auto; margin: 0;">{{ logs.join('\n') || $t('node.detail.empty') }}</pre>
      </div>

      <!-- ============ Settings ============ -->
      <div v-else-if="tab === 'settings'" class="card" style="padding: 16px; display: flex; flex-direction: column; gap: 10px;">
        <div v-for="row in settingsRows" :key="row.k" class="kv-row">
          <span class="k">{{ row.k }}</span><span class="v" :class="{ mono: row.mono }">{{ row.v }}</span>
        </div>
        <hr class="form-divider" />
        <div style="display: flex; gap: 8px; flex-wrap: wrap;">
          <Btn sm @click="drawerOpen = true"><Ico name="edit" :size="14" /> {{ $t('actions.edit') }}</Btn>
          <Btn sm :loading="busy === 'toggleEnable'" @click="bulk(detail.node.enable ? 'disable' : 'enable', 'toggleEnable')">
            {{ detail.node.enable ? $t('disable') : $t('enable') }}
          </Btn>
          <Btn sm :loading="busy === 'toggleSub'" @click="bulk(detail.node.subExclude ? 'subInclude' : 'subExclude', 'toggleSub')">
            {{ detail.node.subExclude ? $t('node.bulk.ops.subInclude') : $t('node.bulk.ops.subExclude') }}
          </Btn>
          <Btn sm :loading="busy === 'rotate'" :disabled="detail.legacy" @click="rotateToken"><Ico name="key" :size="14" /> {{ $t('node.rotateToken') }}</Btn>
        </div>
      </div>

      <!-- ============ Audit / Sync ============ -->
      <div v-else-if="tab === 'audit'" class="card" style="padding: 16px; display: flex; flex-direction: column; gap: 10px;">
        <div style="font-weight: 700;">{{ $t('node.detail.syncState') }}</div>
        <div class="kv-row"><span class="k">{{ $t('node.lastSync') }}</span><span class="v">{{ fmtTime(detail.status.sync?.lastOk) }}</span></div>
        <div class="kv-row"><span class="k">{{ $t('node.detail.lastAttempt') }}</span><span class="v">{{ fmtTime(detail.status.sync?.lastAttempt) }}</span></div>
        <div class="kv-row"><span class="k">{{ $t('node.detail.failures') }}</span><span class="v mono">{{ detail.status.sync?.failures ?? 0 }}</span></div>
        <div v-if="detail.status.sync?.lastError" class="kv-row"><span class="k">{{ $t('node.error') }}</span><span class="v" style="color: var(--rose);">{{ detail.status.sync.lastError }}</span></div>
        <div v-if="detail.status.sync?.nextRetryAt" class="kv-row"><span class="k">{{ $t('node.detail.nextRetry') }}</span><span class="v">{{ fmtTime(detail.status.sync.nextRetryAt) }}</span></div>
        <hr class="form-divider" />
        <div style="display: flex; align-items: center; gap: 8px;">
          <div style="flex: 1; font-weight: 700;">{{ $t('node.detail.audit') }} ({{ opsTotal }})</div>
          <Btn sm :disabled="opsOffset === 0" @click="pageOps(-1)">‹</Btn>
          <Btn sm :disabled="opsOffset + opsLimit >= opsTotal" @click="pageOps(1)">›</Btn>
          <Btn sm :loading="remoteLoading" @click="loadOps"><Ico name="refresh" :size="14" /></Btn>
        </div>
        <div v-if="ops.length === 0" style="color: var(--text-3);">{{ $t('node.detail.empty') }}</div>
        <table v-else class="mn-table">
          <thead><tr><th>{{ $t('node.detail.when') }}</th><th>{{ $t('node.detail.op') }}</th><th>{{ $t('node.detail.actor') }}</th><th>{{ $t('node.detail.result') }}</th></tr></thead>
          <tbody>
            <tr v-for="o in ops" :key="o.id">
              <td>{{ fmtTime(o.startedAt) }}</td>
              <td class="mono">{{ o.op }}{{ o.action ? ':' + o.action : '' }}</td>
              <td>{{ o.actor }}</td>
              <td :style="{ color: o.status === 'ok' ? 'var(--emerald)' : o.status === 'refused' ? 'var(--amber)' : 'var(--rose)' }">
                {{ o.status }} <span v-if="o.error" style="color: var(--text-3);">— {{ o.error }}</span>
                <span class="mono" style="color: var(--text-3); font-size: 11px;"> {{ o.durationMs }}ms ×{{ o.attempts }}</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>
  </div>

  <!-- JSON editor for one object -->
  <Modal :open="editor.open" :title="editor.title" :width="720" @close="editor.open = false">
    <div style="padding: 14px; display: flex; flex-direction: column; gap: 8px;">
      <textarea class="input mono" rows="20" v-model="editor.text" spellcheck="false" />
      <div v-if="editorInvalid" style="color: var(--rose); font-size: 12.5px;">{{ $t('node.detail.invalidJson') }}</div>
      <MHint>{{ $t('node.detail.editorHint') }}</MHint>
    </div>
    <template #footer>
      <Btn @click="editor.open = false">{{ $t('no') }}</Btn>
      <Btn variant="primary" :disabled="editorInvalid" :loading="busy === 'save'" @click="saveObject">{{ $t('actions.save') }}</Btn>
    </template>
  </Modal>

  <!-- confirmation for writes and dangerous ops -->
  <Modal :open="confirm.open" :title="$t('node.detail.confirmTitle')" :width="420" @close="confirm.open = false">
    <div style="padding: 18px; font-size: 13.5px;">{{ confirm.text }}</div>
    <template #footer>
      <Btn @click="confirm.open = false">{{ $t('no') }}</Btn>
      <Btn variant="primary" :loading="!!busy" @click="confirmed">{{ $t('yes') }}</Btn>
    </template>
  </Modal>

  <NodeDrawer :visible="drawerOpen" :id="nodeId" :data="drawerData" @close="onDrawerClose" />
</template>

<script lang="ts" setup>
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import HttpUtils from '@/plugins/httputil'
import { push } from 'notivue'
import Data from '@/store/modules/data'
import { intlLocale } from '@/locales'
import { NodeCapabilities, NodeDetail, NodeOperation } from '@/types/node'
import Btn from '@/components/ui/Btn.vue'
import Ico from '@/components/ui/Ico.vue'
import Chip from '@/components/ui/Chip.vue'
import Tabs from '@/components/ui/Tabs.vue'
import Modal from '@/components/ui/Modal.vue'
import MHint from '@/components/ui/MHint.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import NodeDrawer from '@/layouts/drawers/node/NodeDrawer.vue'

const { t } = useI18n({ useScope: 'global' })
const route = useRoute()
const nodeId = computed(() => Number(route.params.id) || 0)

// ---------------- detail (master-side knowledge, no node call) ----------------
const detail = ref<NodeDetail | null>(null)
const loadingDetail = ref(false)
const detailError = ref('')
const loadDetail = async () => {
  loadingDetail.value = true
  const msg = await HttpUtils.get('api/nodeDetail', { id: nodeId.value })
  loadingDetail.value = false
  if (msg.success && msg.obj) {
    detail.value = msg.obj
    detailError.value = ''
  } else {
    detailError.value = msg.msg
  }
}

const caps = computed((): NodeCapabilities | null => detail.value?.capabilities ?? null)
const health = computed(() => detail.value?.status?.health ?? '')
const healthText = computed(() => {
  switch (health.value) {
    case 'disabled': return t('disable')
    case '': return ''
    default: return t('node.status.' + health.value)
  }
})
const healthColor = computed(() => {
  switch (health.value) {
    case 'online': return 'emerald'
    case 'syncing': return 'cyan'
    case 'degraded': return 'amber'
    case 'offline':
    case 'incompatible': return 'rose'
    default: return undefined
  }
})
const canWrite = computed(() => !!detail.value?.node?.enable && health.value !== 'offline' && health.value !== 'incompatible')

// ---------------- tabs ----------------
const tabKeys = ['overview', 'inbounds', 'outbounds', 'clients', 'routing', 'dns', 'capabilities', 'traffic', 'services', 'logs', 'settings', 'audit']
const tabLabel = (k: string) => t('node.detail.tabs.' + k)
const tabList = computed((): [string, string][] => tabKeys.map((k) => [k, tabLabel(k)]))
const tab = ref(typeof route.query.tab === 'string' && tabKeys.includes(route.query.tab) ? route.query.tab : 'overview')
const outboundKind = ref<'outbounds' | 'endpoints'>('outbounds')
const isObjectTab = computed(() => tab.value === 'inbounds' || tab.value === 'outbounds')
const writeObject = computed(() => (tab.value === 'inbounds' ? 'inbounds' : outboundKind.value))

// ---------------- remote calls ----------------
const remoteLoading = ref(false)
const remoteError = ref('')
const busy = ref('')

const remote = async (op: string, extra: Record<string, any> = {}): Promise<any> => {
  const body: Record<string, any> = { id: nodeId.value, op }
  if (extra.data !== undefined) body.data = typeof extra.data === 'string' ? extra.data : JSON.stringify(extra.data)
  if (extra.params) body.params = JSON.stringify(extra.params)
  if (extra.action) body.action = extra.action
  if (extra.confirm) body.confirm = 'true'
  const msg = await HttpUtils.post('api/nodeRemote', body)
  if (!msg.success) throw new Error(msg.msg)
  return msg.obj
}

const objects = ref<any[]>([])
const logs = ref<string[]>([])
const servicesInfo = ref<Record<string, any>>({})
const sectionText = ref('')
const configCache = ref<any>(null)
const logLevel = ref('info')
const logCount = ref(100)

const loadTab = async () => {
  remoteError.value = ''
  if (!detail.value) return
  if (['overview', 'capabilities', 'traffic', 'settings'].includes(tab.value)) return
  if (tab.value === 'audit') return loadOps()
  remoteLoading.value = true
  try {
    switch (tab.value) {
      case 'inbounds':
      case 'outbounds': {
        const kind = writeObject.value
        const op = kind === 'inbounds' ? 'inbounds' : kind
        const obj = await remote(op)
        objects.value = obj?.[kind] ?? []
        break
      }
      case 'clients': {
        const obj = await remote('clients')
        objects.value = obj?.clients ?? []
        break
      }
      case 'routing':
      case 'dns': {
        const obj = await remote('config')
        configCache.value = obj?.config ?? {}
        const section = tab.value === 'dns' ? 'dns' : 'route'
        sectionText.value = JSON.stringify(configCache.value?.[section] ?? {}, null, 2)
        break
      }
      case 'services': {
        const out: Record<string, any> = {}
        if (!detail.value.legacy) Object.assign(out, await remote('services'))
        const list = await remote('services.list')
        out[t('pages.services')] = (list?.services ?? []).map((s: any) => s.tag + ' (' + s.type + ')').join(', ') || '—'
        servicesInfo.value = out
        break
      }
      case 'logs': {
        const obj = await remote('logs', { params: { count: String(logCount.value), level: logLevel.value } })
        logs.value = Array.isArray(obj) ? obj : []
        break
      }
    }
  } catch (e: any) {
    remoteError.value = e?.message ?? String(e)
    objects.value = []
  } finally {
    remoteLoading.value = false
  }
}

watch([tab, outboundKind], () => {
  objects.value = []
  checkResult.value = ''
  loadTab()
})
watch([logLevel, logCount], () => { if (tab.value === 'logs') loadTab() })

// ---------------- object editor ----------------
const isAdopted = (tag: string) => (detail.value?.adopted ?? []).includes(tag)
const editor = ref({ open: false, title: '', text: '', action: 'new' as 'new' | 'edit' })
const editorInvalid = computed(() => { try { JSON.parse(editor.value.text); return false } catch { return true } })
const templates: Record<string, any> = {
  inbounds: { type: 'vless', tag: 'vless-new', listen: '::', listen_port: 443 },
  outbounds: { type: 'direct', tag: 'direct-new' },
  endpoints: { type: 'wireguard', tag: 'wg-new' },
}
const newObject = () => {
  editor.value = { open: true, title: t('actions.add') + ' · ' + writeObject.value, text: JSON.stringify(templates[writeObject.value], null, 2), action: 'new' }
}
const editObject = (o: any) => {
  editor.value = { open: true, title: t('actions.edit') + ' · ' + o.tag, text: JSON.stringify(o, null, 2), action: 'edit' }
}
const saveObject = async () => {
  busy.value = 'save'
  try {
    await remote('save.' + writeObject.value, { action: editor.value.action, data: editor.value.text })
    push.success({ message: t('success') })
    editor.value.open = false
    await loadTab()
  } catch { /* the toast already shows the node's or the master's refusal */ }
  busy.value = ''
}
const pendingDelete = ref('')
const askDelete = (tag: string) => {
  pendingDelete.value = tag
  ask('delete')
}

// ---------------- outbound check ----------------
const checking = ref('')
const checkResult = ref('')
const checkOutbound = async (tag: string) => {
  checking.value = tag
  try {
    const r = await remote('checkOutbound', { params: { tag } })
    checkResult.value = tag + ': ' + JSON.stringify(r)
  } catch (e: any) {
    checkResult.value = tag + ': ' + (e?.message ?? e)
  }
  checking.value = ''
}

// ---------------- routing / dns ----------------
const sectionInvalid = computed(() => {
  try { const v = JSON.parse(sectionText.value); return typeof v !== 'object' || v === null || Array.isArray(v) } catch { return true }
})
const checkSection = async () => {
  busy.value = 'checkConfig'
  try {
    const full = { ...(configCache.value ?? {}) }
    full[tab.value === 'dns' ? 'dns' : 'route'] = JSON.parse(sectionText.value)
    await remote('checkConfig', { data: full })
    push.success({ message: t('node.detail.checkOk') })
  } catch { /* toast shown */ }
  busy.value = ''
}

// ---------------- confirmations ----------------
const confirm = ref({ open: false, text: '', op: '' })
const ask = (op: string) => {
  const name = detail.value?.node?.name ?? ''
  const texts: Record<string, string> = {
    'core.restart': t('node.detail.confirmCoreRestart', { name }),
    'core.stop': t('node.detail.confirmCoreStop', { name }),
    'core.start': t('node.detail.confirmCoreStart', { name }),
    'panel.restart': t('node.detail.confirmPanelRestart', { name }),
    'config.route': t('node.detail.confirmSection', { name, section: 'route' }),
    'config.dns': t('node.detail.confirmSection', { name, section: 'dns' }),
    delete: t('node.detail.confirmDelete', { name, tag: pendingDelete.value }),
  }
  confirm.value = { open: true, text: texts[op] ?? op, op }
}
const confirmed = async () => {
  const op = confirm.value.op
  busy.value = op
  try {
    if (op === 'delete') {
      await remote('save.' + writeObject.value, { action: 'del', data: JSON.stringify(pendingDelete.value) })
      await loadTab()
    } else if (op === 'config.route' || op === 'config.dns') {
      await remote(op, { data: sectionText.value })
      await loadTab()
    } else {
      await remote(op, { confirm: true })
      setTimeout(loadDetail, 1500)
    }
    push.success({ message: t('success') })
    confirm.value.open = false
  } catch { /* toast shown */ }
  busy.value = ''
}

// ---------------- node-level actions ----------------
const reconnect = async () => {
  busy.value = 'reconnect'
  await HttpUtils.post('api/nodeReconnect', { id: nodeId.value })
  busy.value = ''
  loadDetail()
}
const reconcileNow = async () => {
  busy.value = 'reconcile'
  await HttpUtils.post('api/reconcileNode', { id: nodeId.value })
  busy.value = ''
  loadDetail()
}
const refreshCaps = async () => {
  busy.value = 'capabilities'
  try { await remote('capabilities') } catch { /* toast shown */ }
  busy.value = ''
  loadDetail()
}
const bulk = async (op: string, key: string) => {
  busy.value = key
  await HttpUtils.post('api/nodeBulk', { ids: JSON.stringify([nodeId.value]), op })
  busy.value = ''
  Data().loadData()
  loadDetail()
}
const rotateToken = async () => {
  busy.value = 'rotate'
  await HttpUtils.post('api/nodeTokenRotate', { id: nodeId.value })
  busy.value = ''
  loadDetail()
}

// ---------------- audit ----------------
const ops = ref<NodeOperation[]>([])
const opsTotal = ref(0)
const opsOffset = ref(0)
const opsLimit = 50
const loadOps = async () => {
  remoteLoading.value = true
  const msg = await HttpUtils.get('api/nodeOps', { id: nodeId.value, limit: opsLimit, offset: opsOffset.value })
  remoteLoading.value = false
  if (msg.success && msg.obj) {
    ops.value = msg.obj.rows ?? []
    opsTotal.value = msg.obj.total ?? 0
  }
}
const pageOps = (dir: number) => {
  opsOffset.value = Math.max(0, opsOffset.value + dir * opsLimit)
  loadOps()
}

// ---------------- settings drawer ----------------
const drawerOpen = ref(false)
const drawerData = computed(() => JSON.stringify(detail.value?.node ?? {}))
const onDrawerClose = () => {
  drawerOpen.value = false
  loadDetail()
}

// ---------------- formatting ----------------
const bytes = (n?: number): string => {
  if (!n) return '0 B'
  const u = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let i = 0
  let v = n
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
  return (i === 0 ? v : v.toFixed(1)) + ' ' + u[i]
}
const fmtTime = (unix?: number): string => (unix ? new Date(unix * 1000).toLocaleString(intlLocale()) : t('ui.none'))
const pctOf = (m?: { current: number; total: number }) => (m && m.total > 0 ? Math.round((m.current / m.total) * 100) + '%' : '—')
const dur = (s?: number): string => {
  if (!s) return '—'
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  const m = Math.floor((s % 3600) / 60)
  return (d ? d + 'd ' : '') + h + 'h ' + m + 'm'
}

const overviewRows = computed(() => {
  const st = detail.value?.status
  const n = detail.value?.node
  if (!st || !n) return []
  return [
    { k: t('node.latency'), v: st.latency ? st.latency + ' ' + t('date.ms') : '—', mono: true },
    { k: t('ui.cpu'), v: st.cpu ? Math.round(st.cpu) + '%' : '—', mono: true },
    { k: t('ui.memory'), v: pctOf(st.mem), mono: true },
    { k: 'Swap', v: pctOf(st.swap), mono: true },
    { k: t('node.detail.disk'), v: pctOf(st.disk), mono: true },
    { k: t('node.panelVersion'), v: st.appVersion || '—', mono: true },
    { k: t('node.coreVersion'), v: st.coreVersion || '—', mono: true },
    { k: t('node.detail.coreUptime'), v: dur(st.coreUptime), mono: true },
    { k: t('node.detail.hostUptime'), v: dur(st.hostUptime), mono: true },
    { k: t('node.detail.checkedAt'), v: fmtTime(st.checkedAt) },
    { k: t('node.lastSeen'), v: fmtTime(st.lastOnline || n.lastSeen) },
    { k: t('node.detail.adoptedCount'), v: String(detail.value?.adopted?.length ?? 0), mono: true },
  ]
})

const settingsRows = computed(() => {
  const n = detail.value?.node
  if (!n) return []
  return [
    { k: t('node.baseUrl'), v: n.baseUrl, mono: true },
    { k: t('node.webPath'), v: n.webPath, mono: true },
    { k: t('node.group'), v: n.group || '—' },
    { k: t('node.location'), v: n.location || '—' },
    { k: t('node.syncMode'), v: n.syncMode === 'bidirectional' ? t('node.syncBidirectional') : t('node.syncOneWay') },
    { k: t('node.allowPrivate'), v: n.allowPrivate ? '✓' : '—' },
    { k: t('node.subExclude'), v: n.subExclude ? '✓' : '—' },
    { k: t('node.token'), v: n.tokenSet ? '••••••' : '—' },
    { k: 'TLS', v: n.baseUrl.startsWith('https://') ? (n.certPin ? 'pinned' : n.insecure ? 'skip-verify' : 'verified') : 'plain HTTP' },
  ]
})

const capGroups = computed(() => {
  const c = caps.value
  if (!c) return []
  const tlsList = Object.entries(c.tls ?? {}).filter(([, v]) => v).map(([k]) => k)
  const comps = Object.entries(c.components ?? {}).filter(([, v]) => v).map(([k]) => k)
  return [
    { k: t('pages.inbounds'), v: c.inbounds ?? [] },
    { k: t('node.detail.panelInbounds'), v: c.panelInbounds ?? [] },
    { k: t('pages.outbounds'), v: c.outbounds ?? [] },
    { k: t('pages.endpoints'), v: c.endpoints ?? [] },
    { k: t('pages.services'), v: c.services ?? [] },
    { k: 'DNS', v: c.dnsServers ?? [] },
    { k: t('node.detail.transports'), v: c.transports ?? [] },
    { k: 'TLS', v: tlsList },
    { k: t('node.detail.components'), v: comps },
    { k: t('node.detail.buildTags'), v: c.buildTags ?? [] },
    { k: t('node.detail.management'), v: c.management ?? [] },
  ]
})

onMounted(async () => {
  await loadDetail()
  loadTab()
})
watch(nodeId, async () => {
  detail.value = null
  await loadDetail()
  loadTab()
})
</script>

<style scoped>
.mn-table { width: 100%; border-collapse: collapse; font-size: 12.5px; }
.mn-table th { text-align: start; color: var(--text-3); font-weight: 600; padding: 6px 8px; border-bottom: 1px solid var(--line); }
.mn-table td { padding: 6px 8px; border-bottom: 1px solid var(--line); vertical-align: middle; }
</style>
