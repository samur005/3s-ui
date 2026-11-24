<template>
  <NodeDrawer
    :visible="drawer.visible"
    :id="drawer.id"
    :data="drawer.data"
    @close="drawer.visible = false"
  />
  <NodeImportModal
    :visible="importModal.visible"
    :node-id="importModal.nodeId"
    :node-name="importModal.nodeName"
    @close="importModal.visible = false"
  />
  <SyncConflictsModal :visible="conflictsVisible" @close="conflictsVisible = false" />
  <TombstonesModal :visible="tombstonesVisible" @close="tombstonesVisible = false" />
  <EnrollSyncModal :visible="enrollVisible" @close="enrollVisible = false" />

  <!-- bulk confirmation -->
  <Modal :open="bulkConfirm" :title="$t('node.bulk.confirmTitle')" :width="420" @close="bulkConfirm = false">
    <div style="padding: 18px; font-size: 13.5px; display: flex; flex-direction: column; gap: 8px;">
      <div>{{ bulkOp ? $t('node.bulk.ops.' + bulkOp) : '' }} → {{ selectedNames }}</div>
      <div v-if="bulkOp === 'disable' || bulkOp === 'subExclude'" style="color: var(--amber);">{{ $t('node.bulk.warn') }}</div>
      <div v-for="r in bulkResults" :key="r.nodeId" :style="{ color: r.ok ? 'var(--emerald)' : 'var(--rose)' }" class="mono">
        #{{ r.nodeId }} {{ r.ok ? 'ok' : r.error }}
      </div>
    </div>
    <template #footer>
      <Btn @click="bulkConfirm = false">{{ $t('no') }}</Btn>
      <Btn variant="primary" :loading="bulkRunning" @click="runBulk">{{ $t('yes') }}</Btn>
    </template>
  </Modal>

  <!-- delete confirmation -->
  <DeleteConfirm :open="del.visible" :loading="deleting" @close="del.visible = false" @confirm="confirmDelete" />

  <div class="page-stack fade-up">
    <div class="toolbar" style="justify-content: center; gap: 8px;">
      <Btn variant="primary" sm @click="openDrawer(0)">
        <Ico name="plus" :size="15" /> {{ $t('actions.add') }}
      </Btn>
      <Btn sm @click="conflictsVisible = true">
        <Ico name="refresh" :size="15" /> {{ $t('node.syncConflicts') }}
      </Btn>
      <Btn sm @click="tombstonesVisible = true">
        <Ico name="trash" :size="15" /> {{ $t('node.syncTombstones') || 'Pending deletions' }}
      </Btn>
      <Btn sm @click="enrollVisible = true">
        <Ico name="check" :size="15" /> {{ $t('node.syncEnrollBulk') || 'Enroll for sync' }}
      </Btn>
    </div>

    <!-- filter + bulk -->
    <div v-if="nodes.length > 0" class="toolbar" style="justify-content: center; gap: 8px; flex-wrap: wrap;">
      <select v-if="groups.length > 0" class="input" style="width: auto; min-width: 140px;" v-model="groupFilter">
        <option value="">{{ $t('node.allGroups') }}</option>
        <option v-for="g in groups" :key="g" :value="g">{{ g }}</option>
        <option value="__none__">{{ $t('node.noGroup') }}</option>
      </select>
      <select class="input" style="width: auto; min-width: 140px;" v-model="healthFilter">
        <option value="">{{ $t('node.allStates') }}</option>
        <option v-for="h in healthKeys" :key="h" :value="h">{{ healthLabel(h) }}</option>
      </select>
      <Btn sm :variant="selecting ? 'primary' : undefined" @click="toggleSelecting">
        <Ico name="list" :size="15" /> {{ $t('node.bulk.select') }}
      </Btn>
      <template v-if="selecting">
        <Btn sm @click="selectAllVisible">{{ $t('node.bulk.all') }} ({{ shown.length }})</Btn>
        <span style="font-size: 12.5px; color: var(--text-3); align-self: center;">{{ $t('node.bulk.selected', { n: selected.length }) }}</span>
        <select class="input" style="width: auto; min-width: 170px;" v-model="bulkOp" :disabled="selected.length === 0">
          <option value="">{{ $t('node.bulk.choose') }}</option>
          <option v-for="o in bulkOps" :key="o" :value="o">{{ $t('node.bulk.ops.' + o) }}</option>
        </select>
        <input v-if="bulkOp === 'setGroup'" class="input" style="width: 140px;" maxlength="64" v-model="bulkValue" :placeholder="$t('node.group')" />
        <Btn sm variant="primary" :disabled="!bulkOp || selected.length === 0" @click="bulkConfirm = true">{{ $t('node.bulk.apply') }}</Btn>
      </template>
    </div>

    <EmptyState v-if="nodes.length === 0" icon="server" :title="$t('node.emptyTitle')" :desc="$t('node.emptyDesc')" />
    <EmptyState v-else-if="shown.length === 0" icon="filter" :title="$t('node.noMatch')" :desc="''" />

    <div v-else class="entity-grid">
      <EntityCard
        v-for="item in shown"
        :key="item.id"
        :title="item.name"
        :type="displayUrl(item)"
        :color="stateColor(item)"
        icon="server"
        :rows="cardRows(item)"
      >
        <template #chip>
          <span v-if="selecting" style="cursor: pointer;" @click="toggleSelected(item.id)">
            <Check :checked="selected.includes(item.id)" />
          </span>
          <Chip v-if="item.dirty" color="amber" :title="$t('node.syncDirty')">{{ $t('node.syncDirty') }}</Chip>
          <Chip v-if="stateOf(item) === 'online'" color="emerald" dot>{{ $t('node.status.online') }}</Chip>
          <Chip v-else-if="stateOf(item) === 'degraded'" color="amber" :title="reasonsOf(item)">{{ $t('node.status.degraded') }}</Chip>
          <Chip v-else-if="stateOf(item) === 'syncing'" color="cyan" dot>{{ $t('node.status.syncing') }}</Chip>
          <Chip v-else-if="stateOf(item) === 'incompatible'" color="rose" :title="reasonsOf(item)">{{ $t('node.status.incompatible') }}</Chip>
          <Chip v-else-if="stateOf(item) === 'core-stopped'" color="amber">{{ $t('node.status.coreStopped') }}</Chip>
          <Chip v-else-if="stateOf(item) === 'offline'" color="rose">{{ $t('node.status.offline') }}</Chip>
          <Chip v-else-if="stateOf(item) === 'disabled'">{{ $t('disable') }}</Chip>
          <Chip v-else>{{ $t('node.status.pending') }}</Chip>
        </template>
        <template #actions>
          <CardBtn icon="eye" :title="$t('node.details')" @click="$router.push({ path: '/node', query: { id: item.id } })" />
          <CardBtn icon="edit" border :title="$t('actions.edit')" @click="openDrawer(item.id)" />
          <CardBtn icon="download" border :title="$t('node.import')" @click="openImport(item)" />
          <CardBtn
            icon="refresh"
            border
            :title="$t('node.reconcile')"
            :disabled="reconciling === item.id"
            @click="reconcile(item)"
          />
          <CardBtn icon="trash" border danger :title="$t('actions.del')" @click="askDelete(item.id)" />
        </template>
      </EntityCard>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Data from '@/store/modules/data'
import { intlLocale } from '@/locales'
import { Node, NodeStatus } from '@/types/node'
import Btn from '@/components/ui/Btn.vue'
import Ico from '@/components/ui/Ico.vue'
import Chip from '@/components/ui/Chip.vue'
import DeleteConfirm from '@/components/ui/DeleteConfirm.vue'
import CardBtn from '@/components/ui/CardBtn.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import EntityCard, { EntityRow } from '@/components/ui/EntityCard.vue'
import Modal from '@/components/ui/Modal.vue'
import Check from '@/components/ui/Check.vue'
import NodeDrawer from '@/layouts/drawers/node/NodeDrawer.vue'
import NodeImportModal from '@/layouts/drawers/node/NodeImportModal.vue'
import SyncConflictsModal from '@/layouts/drawers/node/SyncConflictsModal.vue'
import TombstonesModal from '@/layouts/drawers/node/TombstonesModal.vue'
import EnrollSyncModal from '@/layouts/drawers/node/EnrollSyncModal.vue'
import HttpUtils from '@/plugins/httputil'

const conflictsVisible = ref(false)
const tombstonesVisible = ref(false)
const enrollVisible = ref(false)

const { t } = useI18n({ useScope: 'global' })
const dataStore = Data()

const nodes = computed((): Node[] => <Node[]>dataStore.nodes)

// ---------------- live state ----------------
type CardState = 'online' | 'offline' | 'core-stopped' | 'disabled' | 'pending' | 'degraded' | 'syncing' | 'incompatible'

const statusOf = (n: Node): NodeStatus | undefined => dataStore.nodesStatus[n.id]

const stateOf = (n: Node): CardState => {
  if (!n.enable) return 'disabled'
  const st = statusOf(n)
  // No snapshot yet: heartbeat hasn't reached this node since panel start.
  if (!st) return 'pending'
  // core-stopped keeps its own chip; everything else follows the derived health.
  if (st.state === 'core-stopped') return 'core-stopped'
  if (st.health && st.health !== 'online' && st.health !== 'disabled') return st.health
  return st.state
}

const reasonsOf = (n: Node): string => (statusOf(n)?.reasons ?? []).join('\n')

const stateColor = (n: Node): string => {
  switch (stateOf(n)) {
    case 'online': return 'var(--emerald)'
    case 'syncing': return 'var(--cyan)'
    case 'core-stopped':
    case 'degraded': return 'var(--amber)'
    case 'offline':
    case 'incompatible': return 'var(--rose)'
    default: return 'var(--text-3)'
  }
}

// ---------------- filter ----------------
const groupFilter = ref('')
const healthFilter = ref('')
const healthKeys: CardState[] = ['online', 'degraded', 'syncing', 'incompatible', 'core-stopped', 'offline', 'disabled', 'pending']
const healthLabel = (h: string): string => {
  switch (h) {
    case 'core-stopped': return t('node.status.coreStopped')
    case 'disabled': return t('disable')
    default: return t('node.status.' + h)
  }
}
const groups = computed((): string[] =>
  [...new Set(nodes.value.map((n) => (n.group ?? '').trim()).filter((g) => g !== ''))].sort())
const shown = computed((): Node[] => nodes.value.filter((n) => {
  if (groupFilter.value === '__none__' && (n.group ?? '') !== '') return false
  if (groupFilter.value && groupFilter.value !== '__none__' && n.group !== groupFilter.value) return false
  if (healthFilter.value && stateOf(n) !== healthFilter.value) return false
  return true
}))

// ---------------- bulk ----------------
const bulkOps = ['enable', 'disable', 'reconnect', 'reconcile', 'refreshCaps', 'subExclude', 'subInclude', 'setGroup']
const selecting = ref(false)
const selected = ref<number[]>([])
const bulkOp = ref('')
const bulkValue = ref('')
const bulkConfirm = ref(false)
const bulkRunning = ref(false)
const bulkResults = ref<{ nodeId: number; ok: boolean; error?: string }[]>([])
const toggleSelecting = () => {
  selecting.value = !selecting.value
  if (!selecting.value) selected.value = []
}
const toggleSelected = (id: number) => {
  selected.value = selected.value.includes(id) ? selected.value.filter((x) => x !== id) : [...selected.value, id]
}
const selectAllVisible = () => { selected.value = shown.value.map((n) => n.id) }
const selectedNames = computed(() => nodes.value.filter((n) => selected.value.includes(n.id)).map((n) => n.name).join(', '))
watch(bulkConfirm, (v) => { if (v) bulkResults.value = [] })
const runBulk = async () => {
  bulkRunning.value = true
  const msg = await HttpUtils.post('api/nodeBulk', { ids: JSON.stringify(selected.value), op: bulkOp.value, value: bulkValue.value })
  bulkRunning.value = false
  if (msg.success) {
    bulkResults.value = msg.obj ?? []
    dataStore.loadData()
    if (bulkResults.value.every((r) => r.ok)) bulkConfirm.value = false
  }
}

const displayUrl = (n: Node): string => n.baseUrl.replace(/^https?:\/\//, '')

// ---------------- relative time ----------------
const relTime = (unix: number): string => {
  if (!unix) return t('ui.none')
  // built per call so switching language re-formats; hoisting it would pin the
  // formatter to the language the page was opened in
  const rtf = new Intl.RelativeTimeFormat(intlLocale(), { numeric: 'auto' })
  const diff = unix - Math.floor(Date.now() / 1000)
  const abs = Math.abs(diff)
  if (abs < 60) return rtf.format(Math.trunc(diff), 'second')
  if (abs < 3600) return rtf.format(Math.trunc(diff / 60), 'minute')
  if (abs < 86400) return rtf.format(Math.trunc(diff / 3600), 'hour')
  return rtf.format(Math.trunc(diff / 86400), 'day')
}

// ---------------- card rows ----------------
const pct = (v: number): string => Math.round(v) + '%'

const cardRows = (n: Node): EntityRow[] => {
  const rows = baseRows(n)
  const place = [n.group, n.location].filter((v) => v && v.trim() !== '').join(' · ')
  if (place) rows.unshift({ k: t('node.group') + ' / ' + t('node.location'), v: place })
  if (n.subExclude) rows.push({ k: t('node.subExclude'), v: t('node.subExcluded'), color: 'var(--amber)' })
  return rows
}

const baseRows = (n: Node): EntityRow[] => {
  const state = stateOf(n)
  const st = statusOf(n)
  // 待同步的节点把"上次同步"标黄,与卡片顶部的 dirty 徽章呼应
  const lastSync: EntityRow = n.dirty
    ? { k: t('node.lastSync'), v: relTime(n.lastSync ?? 0), color: 'var(--amber)' }
    : { k: t('node.lastSync'), v: relTime(n.lastSync ?? 0) }
  if (st && (state === 'online' || state === 'core-stopped' || state === 'degraded' || state === 'syncing' || state === 'incompatible') && st.state !== 'offline') {
    const memPct = st!.mem.total > 0 ? (st!.mem.current / st!.mem.total) * 100 : 0
    return [
      { k: t('node.latency'), v: st!.latency + ' ' + t('date.ms'), mono: true },
      { k: t('ui.cpu') + ' / ' + t('ui.memory'), v: pct(st!.cpu) + ' / ' + pct(memPct), mono: true },
      { k: t('node.panelVersion'), v: st!.appVersion || t('ui.none'), mono: !!st!.appVersion },
      st!.state === 'online'
        ? { k: t('node.coreVersion'), v: st!.coreVersion || t('ui.none'), mono: !!st!.coreVersion }
        : { k: t('node.coreVersion'), v: t('node.status.coreStopped'), color: 'var(--amber)' },
      lastSync,
    ]
  }
  if (state === 'offline') {
    return [
      { k: t('node.lastSeen'), v: relTime(st?.lastOnline || n.lastSeen) },
      { k: t('node.error'), v: st?.error ?? t('ui.none'), color: 'var(--rose)' },
      { k: t('node.panelVersion'), v: t('ui.none') },
      { k: t('node.coreVersion'), v: t('ui.none') },
      lastSync,
    ]
  }
  // pending / disabled
  return [
    { k: t('node.latency'), v: t('ui.none') },
    { k: t('ui.cpu') + ' / ' + t('ui.memory'), v: t('ui.none') },
    { k: t('node.lastSeen'), v: relTime(n.lastSeen) },
    { k: t('node.coreVersion'), v: t('ui.none') },
    lastSync,
  ]
}

// ---------------- drawer ----------------
const drawer = ref({ visible: false, id: 0, data: '' })
const openDrawer = (id: number) => {
  drawer.value.id = id
  drawer.value.data = id == 0 ? '{}' : JSON.stringify(nodes.value.findLast((n) => n.id == id))
  drawer.value.visible = true
}

// ---------------- import inbounds ----------------
const importModal = ref({ visible: false, nodeId: 0, nodeName: '' })
const openImport = (n: Node) => {
  importModal.value = { visible: true, nodeId: n.id, nodeName: n.name }
}

// ---------------- reconcile ----------------
const reconciling = ref(0)
const reconcile = async (n: Node) => {
  reconciling.value = n.id
  const msg = await HttpUtils.post('api/reconcileNode', { id: n.id })
  reconciling.value = 0
  if (msg.success) {
    dataStore.loadData()
  }
}

// ---------------- delete (with confirm) ----------------
const del = ref({ visible: false, id: 0 })
const deleting = ref(false)
const askDelete = (id: number) => {
  del.value.id = id
  del.value.visible = true
}
const confirmDelete = async () => {
  deleting.value = true
  const success = await dataStore.save('nodes', 'del', del.value.id)
  deleting.value = false
  if (success) del.value.visible = false
}
</script>
