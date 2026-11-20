<template>
  <MDrawer
    :open="visible"
    icon="refresh"
    color="var(--brand)"
    :title="$t('node.syncEnrollBulk') || 'Bidirectional sync membership'"
    :sub="$t('node.syncEnrollBulkHint') || 'Select records to enroll or unenroll'"
    :save-label="$t('actions.save')"
    :width="640"
    :loading="saving"
    @close="$emit('close')"
    @save="apply"
  >
    <Tabs v-model="tab" :tabs="tabs" />

    <div style="display:flex; align-items:center; gap:10px; margin:12px 0; padding:10px; background:var(--surface-2); border-radius:8px;">
      <SwitchLabel v-model="autoEnroll" :label="$t('node.autoEnroll') || 'Auto-enroll new clients and inbounds'" />
    </div>

    <div style="display:flex; gap:8px; margin:12px 0;">
      <Btn sm :variant="mode === 'enroll' ? 'primary' : 'subtle'" @click="mode = 'enroll'">
        {{ $t('node.syncEnroll') || 'Enroll' }}
      </Btn>
      <Btn sm :variant="mode === 'unenroll' ? 'primary' : 'subtle'" @click="mode = 'unenroll'">
        {{ $t('node.syncUnenroll') || 'Unenroll' }}
      </Btn>
    </div>

    <div v-if="tab === 'inbounds'">
      <div style="display:flex; gap:8px; margin-bottom:10px;">
        <Btn sm @click="selectAllInbounds">{{ $t('actions.selectAll') || 'Select all' }}</Btn>
        <Btn sm variant="subtle" @click="selectedInbounds = []">{{ $t('actions.clear') || 'Clear' }}</Btn>
      </div>
      <div v-for="ib in filteredInbounds" :key="ib.id" class="row">
        <label style="display:flex; align-items:center; gap:10px; cursor:pointer; width:100%;">
          <input type="checkbox" :value="ib.id" v-model="selectedInbounds" />
          <span class="mono" style="flex:1;">{{ ib.tag }}</span>
          <span style="font-size:12px; color:var(--text-3);">{{ ib.type }}</span>
          <Chip v-if="ib.syncUid" color="emerald" sm>{{ $t('node.enrolled') || 'enrolled' }}</Chip>
        </label>
      </div>
    </div>

    <div v-else>
      <div style="display:flex; gap:8px; margin-bottom:10px;">
        <Btn sm @click="selectAllClients">{{ $t('actions.selectAll') || 'Select all' }}</Btn>
        <Btn sm variant="subtle" @click="selectedClients = []">{{ $t('actions.clear') || 'Clear' }}</Btn>
      </div>
      <div v-for="c in filteredClients" :key="c.id" class="row">
        <label style="display:flex; align-items:center; gap:10px; cursor:pointer; width:100%;">
          <input type="checkbox" :value="c.id" v-model="selectedClients" />
          <span style="flex:1;">{{ c.name }}</span>
          <span class="mono" style="font-size:12px; color:var(--text-3);">{{ c.group || '—' }}</span>
          <Chip v-if="c.syncUid" color="emerald" sm>{{ $t('node.enrolled') || 'enrolled' }}</Chip>
        </label>
      </div>
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
import SwitchLabel from '@/components/ui/SwitchLabel.vue'

const props = defineProps<{ visible: boolean }>()
defineEmits<{ close: [] }>()

const { t } = useI18n({ useScope: 'global' })
const tab = ref('inbounds')
const mode = ref<'enroll' | 'unenroll'>('enroll')
const autoEnroll = ref(false)
const saving = ref(false)
const selectedInbounds = ref<number[]>([])
const selectedClients = ref<number[]>([])

const tabs = computed(() => [
  ['inbounds', t('ui.nav.inbounds') || 'Inbounds'],
  ['clients', t('ui.nav.clients') || 'Clients'],
])

const localInbounds = computed(() => (Data().inbounds ?? []).filter((i: any) => !i.node_id))
const localClients = computed(() => (Data().clients ?? []).filter((c: any) => c.group !== '@cluster'))

const filteredInbounds = computed(() =>
  mode.value === 'unenroll'
    ? localInbounds.value.filter((i: any) => i.syncUid)
    : localInbounds.value
)
const filteredClients = computed(() =>
  mode.value === 'unenroll'
    ? localClients.value.filter((c: any) => c.syncUid)
    : localClients.value
)

const selectAllInbounds = () => {
  selectedInbounds.value = filteredInbounds.value.map((i: any) => i.id)
}
const selectAllClients = () => {
  selectedClients.value = filteredClients.value.map((c: any) => c.id)
}

watch(() => props.visible, async (v) => {
  if (v) {
    selectedInbounds.value = []
    selectedClients.value = []
    mode.value = 'enroll'
    const msg = await HttpUtils.post('api/getAutoEnroll', {})
    autoEnroll.value = msg?.obj?.value === 'true'
  }
})

watch(autoEnroll, async (v) => {
  await HttpUtils.post('api/setAutoEnroll', { value: v ? 'true' : 'false' })
})

const apply = async () => {
  saving.value = true
  const action = mode.value === 'enroll' ? 'enrollSync' : 'unenrollSync'
  const tasks: Promise<any>[] = []
  for (const id of selectedInbounds.value) {
    tasks.push(HttpUtils.post('api/' + action, { kind: 'inbound', id }))
  }
  for (const id of selectedClients.value) {
    tasks.push(HttpUtils.post('api/' + action, { kind: 'client', id }))
  }
  await Promise.all(tasks)
  saving.value = false
  await Data().loadData(true)
}
</script>

<style scoped>
.row {
  padding: 8px 0;
  border-bottom: 1px solid var(--border);
}
</style>
