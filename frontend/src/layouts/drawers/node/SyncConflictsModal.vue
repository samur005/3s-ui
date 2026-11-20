<template>
  <MDrawer
    :open="visible"
    icon="refresh"
    color="var(--amber)"
    :title="$t('node.syncConflicts')"
    :sub="$t('node.syncBiDiWarn')"
    :save-label="$t('actions.refresh')"
    :width="640"
    :loading="loading"
    @close="$emit('close')"
    @save="load"
  >
    <div v-if="loading" class="mono" style="font-size:12px;">…</div>
    <div v-else-if="!rows.length" style="font-size:13px; color: var(--text-3);">{{ $t('ui.none') }}</div>
    <div v-for="r in rows" :key="r.id" class="conflict-row">
      <div style="display:flex; justify-content:space-between; align-items:center;">
        <div class="mono" style="font-size:12.5px; font-weight:600;">{{ r.kind }} · {{ r.uid.slice(0, 12) }}…</div>
        <div style="font-size:11px; color:var(--text-3);">{{ new Date(r.createdAt * 1000).toLocaleString() }}</div>
      </div>
      <div style="display:flex; gap:12px; margin-top:10px; font-size:12px;">
        <div style="flex:1;">
          <div style="color:var(--text-3); margin-bottom:4px;">Local</div>
          <pre class="mono preview">{{ pretty(r.localJson) }}</pre>
        </div>
        <div style="flex:1;">
          <div style="color:var(--text-3); margin-bottom:4px;">Remote</div>
          <pre class="mono preview">{{ pretty(r.remoteJson) }}</pre>
        </div>
      </div>
      <div style="display:flex; gap:8px; margin-top:12px;">
        <Btn sm @click="resolve(r.id, 'local')">{{ $t('node.syncResolveLocal') }}</Btn>
        <Btn sm variant="primary" @click="resolve(r.id, 'remote')">{{ $t('node.syncResolveRemote') }}</Btn>
        <Btn sm variant="subtle" @click="resolve(r.id, 'defer')">{{ $t('node.syncResolveDefer') || 'Defer' }}</Btn>
      </div>
    </div>
  </MDrawer>
</template>

<script lang="ts" setup>
import { ref, watch } from 'vue'
import HttpUtils from '@/plugins/httputil'
import MDrawer from '@/components/ui/MDrawer.vue'
import Btn from '@/components/ui/Btn.vue'

const props = defineProps<{ visible: boolean }>()
defineEmits<{ close: [] }>()

const rows = ref<any[]>([])
const loading = ref(false)

const pretty = (s: string) => {
  try { return JSON.stringify(JSON.parse(s), null, 2).slice(0, 400) } catch { return s }
}

const load = async () => {
  loading.value = true
  const msg = await HttpUtils.post('api/listSyncConflicts', {})
  rows.value = (msg?.success && Array.isArray(msg.obj)) ? msg.obj : []
  loading.value = false
}

watch(() => props.visible, (v) => { if (v) load() })

const resolve = async (id: number, decision: string) => {
  const msg = await HttpUtils.post('api/resolveSyncConflict', { id, decision })
  if (msg?.success) await load()
}
</script>

<style scoped>
.conflict-row {
  padding: 14px 0;
  border-bottom: 1px solid var(--border);
}
.preview {
  font-size: 11px;
  max-height: 120px;
  overflow: auto;
  background: var(--surface-2);
  padding: 8px;
  border-radius: 6px;
  white-space: pre-wrap;
  word-break: break-all;
}
</style>
