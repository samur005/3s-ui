<template>
  <MDrawer
    :open="visible"
    icon="refresh"
    color="var(--brand)"
    :title="$t('node.syncEnrollBulk') || 'Enroll for bidirectional sync'"
    :sub="$t('node.syncEnrollBulkHint') || 'Select inbounds and clients to include in bidirectional sync'"
    :save-label="$t('actions.save')"
    :width="640"
    :loading="saving"
    @close="$emit('close')"
    @save="enrollSelected"
  >
    <Tabs v-model="tab" :tabs="tabs" />

    <div v-if="tab === 'inbounds'" style="margin-top: 12px;">
      <div style="display:flex; gap:8px; margin-bottom:10px;">
        <Btn sm @click="selectAllInbounds">{{ $t('actions.selectAll') || 'Select all' }}</Btn>
        <Btn sm variant="subtle" @click="selectedInbounds = []">{{ $t('actions.clear') || 'Clear' }}</Btn>
      </div>
      <div v-for="ib in localInbounds" :key="ib.id" class="row">
        <label style="display:flex; align-items:center; gap:10px; cursor:pointer; width:100%;">
          <input type="checkbox" :value="ib.id" v-model="selectedInbounds" />
          <span class="mono" style="flex:1;">{{ ib.tag }}</span>
          <span style="font-size:12px; color:var(--text-3);">{{ ib.type }}</span>
          <Chip v-if="ib.syncUid" color="emerald" sm>{{ $t('node.enrolled') || 'enrolled' }}</Chip>
        </label>
      </div>
      <div v-if="!localInbounds.length" style="color:var(--text-3); font-size:13px;">{{ $t('ui.none') }}</div>
    </div>

    <div v-else style="margin-top: 12px;">
      <div style="display:flex; gap:8px; margin-bottom:10px;">
        <Btn sm @click="selectAllClients">{{ $t('actions.selectAll') || 'Select all' }}</Btn>
        <Btn sm variant="subtle" @click="selectedClients = []">{{ $t('actions.clear') || 'Clear' }}</Btn>
      </div>
      <div v-for="c in localClients" :key="c.id" class="row">
        <label style="display:flex; align-items:center; gap:10px; cursor:pointer; width:100%;">
          <input type="checkbox" :value="c.id" v-model="selectedClients" />
          <span style="flex:1;">{{ c.name }}</span>
          <span class="mono" style="font-size:12px; color:var(--text-3);">{{ c.group || '—' }}</span>
          <Chip v-if="c.syncUid" color="emerald" sm>{{ $t('node.enrolled') || 'enrolled' }}</Chip>
        </label>
      </div>
      <div v-if="!localClients.length" style="color:var(--text-3); font-size:13px;">{{ $t('ui.none') }}</div>
    </div>
  </MDrawer>
</template>

<script lang="ts" setup>
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Data from '@/store/modules/data'
import HttpUtils from '@/plugins/httputil'
import MDrawer from '@/components/ui/MDrawer.vue'
import Tabs from '@/components/ui/Tabs.vue'
import Btn from '@/components/ui/Btn.vue'
import Chip from '@/components/ui/Chip.vue'

const props = defineProps<{ visible: boolean }>()
defineEmits<{ close: [] }>()

const { t } = useI18n({ useScope: 'global' })
const tab = ref('inbounds')
const saving = ref(false)
const selectedInbounds = ref<number[]>([])
const selectedClients = ref<number[]>([])

const tabs = computed(() => [
  ['inbounds', t('ui.nav.inbounds') || 'Inbounds'],
  ['clients', t('ui.nav.clients') || 'Clients'],
])

const localInbounds = computed(() =>
  (Data().inbounds ?? []).filter((i: any) => !i.node_id)
)
const localClients = computed(() =>
  (Data().clients ?? []).filter((c: any) => c.group !== '@cluster')
)

const selectAllInbounds = () => {
  selectedInbounds.value = localInbounds.value.map((i: any) => i.id)
}
const selectAllClients = () => {
  selectedClients.value = localClients.value.map((c: any) => c.id)
}

watch(() => props.visible, (v) => {
  if (v) {
    selectedInbounds.value = []
    selectedClients.value = []
  }
})

const enrollSelected = async () => {
  saving.value = true
  const tasks: Promise<any>[] = []
  for (const id of selectedInbounds.value) {
    tasks.push(HttpUtils.post('api/enrollSync', { kind: 'inbound', id }))
  }
  for (const id of selectedClients.value) {
    tasks.push(HttpUtils.post('api/enrollSync', { kind: 'client', id }))
  }
  await Promise.all(tasks)
  saving.value = false
  // Refresh data so enrolled badges appear
  await Data().loadData(true)
}
</script>

<style scoped>
.row {
  padding: 8px 0;
  border-bottom: 1px solid var(--border);
}
</style>
