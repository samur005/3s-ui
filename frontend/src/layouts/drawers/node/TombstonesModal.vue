<template>
  <MDrawer
    :open="visible"
    icon="trash"
    color="var(--rose)"
    :title="$t('node.syncTombstones') || 'Pending deletions'"
    :sub="$t('node.syncTombstoneHint') || 'Confirm permanent removal of tombstoned records'"
    :save-label="$t('actions.refresh')"
    :width="520"
    :loading="loading"
    @close="$emit('close')"
    @save="load"
  >
    <div v-if="loading" class="mono" style="font-size:12px;">…</div>
    <div v-else-if="!rows.length" style="font-size:13px; color: var(--text-3);">{{ $t('ui.none') }}</div>
    <div v-for="r in rows" :key="r.kind + r.id" class="tomb-row">
      <div style="display:flex; justify-content:space-between; align-items:center;">
        <div>
          <span class="mono" style="font-size:12.5px; font-weight:600;">{{ r.kind }} · {{ r.name }}</span>
          <div class="mono" style="font-size:11px; color:var(--text-3);">{{ r.uid.slice(0, 12) }}…</div>
        </div>
        <div style="font-size:11px; color:var(--text-3);">{{ new Date(r.deletedAt * 1000).toLocaleDateString() }}</div>
      </div>
      <div style="margin-top:10px;">
        <Btn sm variant="primary" :loading="purging === r.id" @click="purge(r)">
          {{ $t('node.syncConfirmDelete') || 'Confirm permanent delete' }}
        </Btn>
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
const purging = ref(0)

const load = async () => {
  loading.value = true
  const msg = await HttpUtils.post('api/listTombstones', {})
  rows.value = (msg?.success && Array.isArray(msg.obj)) ? msg.obj : []
  loading.value = false
}

watch(() => props.visible, (v) => { if (v) load() })

const purge = async (r: any) => {
  purging.value = r.id
  const msg = await HttpUtils.post('api/purgeTombstone', { kind: r.kind, id: r.id })
  purging.value = 0
  if (msg?.success) await load()
}
</script>

<style scoped>
.tomb-row {
  padding: 14px 0;
  border-bottom: 1px solid var(--border);
}
</style>
