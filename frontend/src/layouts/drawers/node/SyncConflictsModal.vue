<template>
  <MDrawer
    :open="visible"
    icon="refresh"
    color="var(--amber)"
    :title="$t('node.syncConflicts')"
    :sub="$t('node.syncBiDiWarn')"
    :save-label="$t('actions.refresh')"
    :width="560"
    :loading="loading"
    @close="$emit('close')"
    @save="load"
  >
    <div v-if="loading" class="mono" style="font-size:12px;">…</div>
    <div v-else-if="!rows.length" style="font-size:13px; color: var(--text-3);">—</div>
    <div v-for="r in rows" :key="r.id" class="conflict-row">
      <div class="mono" style="font-size:12px;">{{ r.kind }} · {{ r.uid }}</div>
      <div style="display:flex; gap:8px; margin-top:8px;">
        <Btn sm @click="resolve(r.id, 'local')">{{ $t('node.syncResolveLocal') }}</Btn>
        <Btn sm variant="primary" @click="resolve(r.id, 'remote')">{{ $t('node.syncResolveRemote') }}</Btn>
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
  padding: 12px 0;
  border-bottom: 1px solid var(--border);
}
</style>
