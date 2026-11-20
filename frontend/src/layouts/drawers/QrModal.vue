<template>
  <Modal :open="visible" title="QR" :width="400" @close="$emit('close')">
    <div id="qrcode-modal" style="padding: 0 20px 18px;">
      <Tabs v-model="tab" :tabs="tabs" />
      <div v-if="loading" style="padding: 40px 0; text-align: center; color: var(--text-3); font-size: 13px;">
        {{ $t('loading') }}
      </div>
      <template v-else>
        <div v-if="tab === 'sub'" class="qr-list">
          <div class="qr-item" v-for="q in subQrs" :key="q.label">
            <Chip>{{ q.label }}</Chip>
            <QrcodeVue
              :value="q.value"
              :size="size"
              :margin="1"
              :style="{ borderRadius: '1rem', cursor: q.copy ? 'copy' : 'not-allowed' }"
              @click="q.copy && copy(q.value)"
            />
          </div>
        </div>
        <div v-else-if="tab === 'wg'" class="qr-list">
          <div class="qr-item" v-for="e in wgEntries" :key="e.inboundId">
            <Chip :color="e.enable ? 'emerald' : ''">{{ e.tag }} · {{ e.type === 'awg' ? 'AmneziaWG ' + (e.version ?? '') : 'WireGuard' }}</Chip>
            <QrcodeVue
              v-if="wgQr(e)"
              :value="wgQr(e)"
              :size="size"
              :margin="1"
              level="L"
              style="border-radius: .5rem; cursor: copy;"
              @click="copy(wgQr(e))"
            />
            <div v-else class="wg-note">{{ $t('wg.qrTooLong') }}</div>
            <div class="wg-actions">
              <Btn sm @click="copy(e.conf)">{{ $t('wg.copyConf') }}</Btn>
              <Btn sm @click="download(e)">{{ $t('wg.downloadConf') }}</Btn>
              <Btn v-if="e.vpn" sm @click="copy(e.vpn)">{{ $t('wg.copyVpn') }}</Btn>
              <Btn v-if="e.uri" sm @click="copy(e.uri)">{{ $t('wg.copyLink') }}</Btn>
            </div>
            <div v-if="!e.enable" class="wg-note">{{ $t('wg.inboundOff') }}</div>
          </div>
        </div>
        <div v-else class="qr-list">
          <div class="qr-item" v-for="l in clientLinks" :key="l.uri">
            <Chip>{{ l.remark ?? $t('client.' + l.type) }}</Chip>
            <QrcodeVue :value="l.uri" :size="size" :margin="1" style="border-radius: .5rem; cursor: copy;" @click="copy(l.uri)" />
          </div>
        </div>
      </template>
    </div>
  </Modal>
</template>

<script lang="ts" setup>
import { computed, ref, watch } from 'vue'
import QrcodeVue from 'qrcode.vue'
import { useI18n } from 'vue-i18n'
import Data from '@/store/modules/data'
import { copyToClipboard } from '@/plugins/clipboard'
import Modal from '@/components/ui/Modal.vue'
import Tabs from '@/components/ui/Tabs.vue'
import Chip from '@/components/ui/Chip.vue'
import Btn from '@/components/ui/Btn.vue'
import HttpUtils from '@/plugins/httputil'

const props = defineProps<{
  visible: boolean
  id: number
}>()
defineEmits<{ close: [] }>()

const { t } = useI18n({ useScope: 'global' })

const tab = ref('sub')
const client = ref<any>({})
const loading = ref(false)
// The client's configs on the WireGuard / AmneziaWG inbounds it is bound to.
const wgEntries = ref<any[]>([])

const tabs = computed((): [string, string][] => {
  const list: [string, string][] = [['sub', t('setting.sub')], ['link', t('client.links')]]
  if (wgEntries.value.length) list.push(['wg', 'WG / AWG'])
  return list
})

const load = async () => {
  loading.value = true
  wgEntries.value = []
  client.value = await Data().loadClients(props.id)
  const msg = await HttpUtils.get('api/wgClient', { id: props.id })
  if (msg.success && Array.isArray(msg.obj)) wgEntries.value = msg.obj
  loading.value = false
}

// What the QR carries: the wireguard:// link for WireGuard, the config text for
// AmneziaWG (the Amnezia app reads it). Past what a QR holds there is none, and
// the copy and download buttons stay.
const QR_MAX = 2300
const wgQr = (e: any): string => {
  const v = e.uri || e.conf || ''
  return v.length <= QR_MAX ? v : ''
}
const download = (e: any) => {
  const url = URL.createObjectURL(new Blob([e.conf], { type: 'text/plain;charset=utf-8' }))
  const a = document.createElement('a')
  a.href = url
  a.download = e.filename || 'client.conf'
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
}

// The name is a path segment and may hold anything but nothing at all, so it
// is encoded: a '#', '?' or space in it used to cut the URL short, and every
// QR below -- the plain one, and Happ's, which takes the URL unencoded after
// happ://add/ -- fetched another path. The server decodes it back.
const clientSub = computed(() => Data().subURI + encodeURIComponent(client.value.name ?? ''))
const singbox = computed(() => {
  const url = clientSub.value + '?format=json'
  return 'sing-box://import-remote-profile?url=' + encodeURIComponent(url) + '#' + (client.value.name ?? '')
})
// Happ takes the subscription URL as it is, unencoded, after happ://add/.
// ?format=happ is what Happ's own User-Agent gets on the bare URL; naming it
// keeps the import right from a scanner that sends some other one.
const happ = computed(() => 'happ://add/' + clientSub.value + '?format=happ')
const subQrs = computed(() => [
  { label: t('setting.sub'), value: clientSub.value, copy: true },
  { label: t('setting.pageSub'), value: clientSub.value + '?format=page', copy: true },
  { label: t('setting.jsonSub'), value: clientSub.value + '?format=json', copy: true },
  { label: t('setting.clashSub'), value: clientSub.value + '?format=clash', copy: true },
  { label: 'SING-BOX', value: singbox.value, copy: false },
  { label: 'HAPP', value: happ.value, copy: false },
])
const clientLinks = computed(() => client.value.links ?? [])

const size = computed(() => {
  if (window.innerWidth > 380) return 280
  if (window.innerWidth > 330) return 260
  return 230
})

const copy = (txt: string) => copyToClipboard(txt, 'qrcode-modal')

watch(() => props.visible, (v) => {
  if (v) {
    tab.value = 'sub'
    load()
  }
})
</script>

<style scoped>
.qr-list { display: flex; flex-direction: column; gap: 18px; }
.wg-actions { display: flex; flex-wrap: wrap; gap: 6px; justify-content: center; }
.wg-note { font-size: 12px; color: var(--text-3); text-align: center; }
.qr-item { display: flex; flex-direction: column; align-items: center; gap: 9px; }
</style>
