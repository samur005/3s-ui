<template>
  <div class="page-stack-lg fade-up" style="max-width: 1040px;">
    <!-- header: state + actions -->
    <div class="head-row">
      <div style="display: flex; align-items: center; gap: 8px; flex-wrap: wrap; flex: 1 1 auto; min-width: 0;">
        <Chip v-if="!status.supported" color="amber">{{ $t('qwdtt.linuxOnly') }}</Chip>
        <template v-else>
          <Chip v-if="status.running" color="emerald" dot>{{ $t('qwdtt.running') }}</Chip>
          <Chip v-else-if="status.enabled" color="rose" dot>{{ $t('qwdtt.stopped') }}</Chip>
          <Chip v-else>{{ $t('qwdtt.off') }}</Chip>
          <Chip v-if="status.running && cfg.routeThroughCore" :color="status.routed ? 'emerald' : 'amber'">
            {{ status.routed ? $t('qwdtt.routed') : $t('qwdtt.notRouted') }}
          </Chip>
          <Chip v-if="status.running">pid {{ status.pid }} · {{ uptime }}</Chip>
        </template>
      </div>
      <div class="head-actions">
        <Btn sm @click="$router.push('/inbounds')">
          <Ico name="inbound" :size="15" /> {{ $t('qwdtt.openInbounds') }}
        </Btn>
        <Btn sm :loading="restarting" :disabled="!cfg.enabled" @click="restart">
          <Ico name="refresh" :size="15" /> {{ $t('qwdtt.restart') }}
        </Btn>
      </div>
    </div>

    <div v-if="!inboundId" class="fieldnote warn">
      <span>{{ $t('qwdtt.noInbound') }}</span>
    </div>
    <div v-else class="fieldnote">
      <span>{{ $t('qwdtt.inboundHint') }}</span>
    </div>

    <div v-if="status.enabled && status.error" class="fieldnote warn">
      <span>{{ status.error }}</span>
    </div>
    <div v-if="status.enabled && status.supported && !status.binaryFound" class="fieldnote warn">
      <span>{{ $t('qwdtt.binaryMissing', { path: status.binary }) }}</span>
    </div>

    <!-- ===================== VK hashes ===================== -->
    <SettingsGroup :title="$t('qwdtt.vk')" :desc="$t('qwdtt.vkDesc')">
      <div v-if="status.hashEnv" class="fieldnote warn" style="margin-bottom: 12px;">
        <span>{{ $t('qwdtt.hashEnv') }}</span>
      </div>

      <div v-if="hashList.length && !hashChecks.length" class="hash-list">
        <div v-for="h in hashList" :key="h" class="hash-row" style="flex-direction: column; align-items: flex-start; gap: 6px;">
          <span class="mono hash-name">{{ h }}</span>
          <div style="display: flex; gap: 6px;">
            <Btn sm :loading="hashBusy[h]" @click="endCall(h)">{{ $t('qwdtt.endCall') }}</Btn>
            <Btn sm style="color: var(--rose);" :loading="hashBusy[h]" @click="deleteHash(h)">{{ $t('qwdtt.deleteHash') }}</Btn>
          </div>
        </div>
      </div>
      <div v-else class="fieldnote"><span>{{ $t('qwdtt.noHashes') }}</span></div>

      <div v-if="hashChecks.length" class="hash-list">
        <div v-for="h in hashChecks" :key="h.hash" class="hash-row" style="flex-direction: column; align-items: flex-start; gap: 6px;">
          <div style="display: flex; align-items: center; gap: 8px; flex-wrap: wrap;">
            <span class="mono hash-name">{{ h.hash }}</span>
            <Chip v-if="h.alive === true" color="emerald" dot>{{ $t('qwdtt.alive') }}</Chip>
            <Chip v-else-if="h.alive === false" color="rose" dot>{{ $t('qwdtt.dead') }}</Chip>
            <Chip v-else color="amber" :title="h.error">{{ $t('qwdtt.unknown') }}</Chip>
            <Chip v-if="h.generated">{{ $t('qwdtt.generatedByPanel') }}</Chip>
          </div>
          <div style="display: flex; gap: 6px;">
            <Btn sm :loading="hashBusy[h.hash]" @click="endCall(h.hash)">{{ $t('qwdtt.endCall') }}</Btn>
            <Btn sm style="color: var(--rose);" :loading="hashBusy[h.hash]" @click="deleteHash(h.hash)">{{ $t('qwdtt.deleteHash') }}</Btn>
          </div>
        </div>
      </div>

      <div class="btn-row">
        <Btn variant="primary" sm :loading="generating" :disabled="status.hashEnv || !canGen || !inboundId" @click="generate(false)">
          <Ico name="plus" :size="15" /> {{ $t('qwdtt.generate') }}
        </Btn>
        <Btn sm :loading="generating" :disabled="status.hashEnv || !canGen || hashCount < 4 || !inboundId" @click="generate(true)">
          <Ico name="refresh" :size="15" /> {{ $t('qwdtt.generateReplace') }}
        </Btn>
        <Btn sm :loading="checking" :disabled="hashCount === 0" @click="checkHashes">
          <Ico name="bolt" :size="15" /> {{ $t('qwdtt.checkHashes') }}
        </Btn>
      </div>
      <div v-if="!canGen" class="fieldnote" style="margin-top: 10px;">
        <span>{{ $t('qwdtt.needCookies') }}</span>
      </div>

    </SettingsGroup>

    <SettingsGroup :title="$t('qwdtt.cookies')" :desc="$t('qwdtt.cookiesDesc')">
      <div style="display: flex; align-items: center; gap: 8px; margin-bottom: 10px;">
        <Chip v-if="status.hasCookies" color="emerald" dot>{{ $t('qwdtt.cookiesStored') }}</Chip>
        <Chip v-else>{{ $t('qwdtt.cookiesNone') }}</Chip>
        <Chip v-if="cookiesOk === true" color="emerald">{{ $t('qwdtt.cookiesValid') }}</Chip>
        <Chip v-else-if="cookiesOk === false" color="rose" :title="cookiesErr">{{ $t('qwdtt.cookiesInvalid') }}</Chip>
      </div>
      <textarea class="input mono" rows="5" v-model="cookiesText" spellcheck="false" autocomplete="off"
                :placeholder="$t('qwdtt.cookiesPlaceholder')" style="font-size: 12px;" />
      <div style="margin-top: 6px; font-size: 12px; color: var(--text-3);">
        Сохранённые cookies отображаются выше. Можно скопировать.
      </div>
      <div class="btn-row">
        <Btn variant="primary" sm :loading="cookieBusy" :disabled="!cookiesText.trim()" @click="saveCookies">
          <Ico name="check" :size="15" /> {{ $t('qwdtt.cookiesSave') }}
        </Btn>
        <Btn sm :loading="cookieBusy" :disabled="!status.hasCookies" @click="checkCookies">
          <Ico name="bolt" :size="15" /> {{ $t('qwdtt.cookiesCheck') }}
        </Btn>
        <Btn sm :loading="cookieBusy" :disabled="!status.hasCookies" style="color: var(--rose);" @click="clearCookies">
          <Ico name="trash" :size="15" /> {{ $t('qwdtt.cookiesClear') }}
        </Btn>
      </div>
    </SettingsGroup>

    <!-- ===================== remote WDTT hash source ===================== -->
    <SettingsGroup :title="$t('qwdtt.remote')" :desc="$t('qwdtt.remoteDesc')">
      <div style="display: flex; align-items: center; gap: 8px; margin-bottom: 10px; flex-wrap: wrap;">
        <Chip v-if="remote.url" color="emerald" dot>{{ $t('qwdtt.remoteSet') }}</Chip>
        <Chip v-else>{{ $t('qwdtt.remoteNone') }}</Chip>
        <Chip v-if="remote.fromEnv" color="amber">{{ $t('qwdtt.remoteEnv') }}</Chip>
      </div>
      <div class="grid2">
        <input class="input mono" v-model="remoteForm.url" :disabled="remote.fromEnv" placeholder="https://wdtt.example.com/secret/" />
        <input class="input mono" v-model="remoteForm.username" :disabled="remote.fromEnv" :placeholder="$t('qwdtt.remoteUser')" autocomplete="off" />
        <input class="input mono" type="password" v-model="remoteForm.password" :disabled="remote.fromEnv" autocomplete="new-password"
               :placeholder="remote.hasPassword ? $t('qwdtt.remoteKeep') : $t('qwdtt.remotePass')" />
        <input class="input mono" type="password" v-model="remoteForm.token" :disabled="remote.fromEnv" autocomplete="off"
               :placeholder="remote.hasToken ? $t('qwdtt.remoteKeep') : $t('qwdtt.remoteToken')" />
      </div>
      <div class="btn-row">
        <Btn variant="primary" sm :loading="remoteBusy" :disabled="remote.fromEnv" @click="saveRemote(false)">
          <Ico name="check" :size="15" /> {{ $t('qwdtt.remoteSave') }}
        </Btn>
        <Btn sm :loading="remoteBusy" :disabled="remote.fromEnv || !remote.url" style="color: var(--rose);" @click="saveRemote(true)">
          <Ico name="trash" :size="15" /> {{ $t('qwdtt.remoteClear') }}
        </Btn>
      </div>
    </SettingsGroup>

    <!-- ===================== users on the server ===================== -->
    <SettingsGroup :title="$t('qwdtt.users')" :desc="cfg.userMode === 'shared' ? $t('qwdtt.usersShared') : $t('qwdtt.usersDesc')">
      <div class="btn-row" style="margin-top: 0; margin-bottom: 10px;">
        <Btn sm :loading="usersLoading" @click="loadUsers"><Ico name="refresh" :size="15" /> {{ $t('qwdtt.refresh') }}</Btn>
        <Chip v-if="nodeInbounds">{{ $t('qwdtt.nodeInbounds', { n: nodeInbounds }) }}</Chip>
      </div>
      <table v-if="users.length" class="users">
        <thead>
          <tr>
            <th>{{ $t('qwdtt.userClient') }}</th><th>{{ $t('qwdtt.userDevices') }}</th>
            <th>↑</th><th>↓</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="u in users" :key="u.client + u.managed">
            <td><span class="mono">{{ u.client || '—' }}</span> <Chip v-if="!u.managed">{{ $t('qwdtt.userExternal') }}</Chip></td>
            <td>{{ u.devices }} / {{ u.maxDevices > 1 ? u.maxDevices : 1 }}</td>
            <td>{{ bytes(u.up) }}</td><td>{{ bytes(u.down) }}</td>
          </tr>
        </tbody>
      </table>
      <div v-else class="fieldnote"><span>{{ $t('qwdtt.noUsers') }}</span></div>
    </SettingsGroup>

    <!-- ===================== share ===================== -->
    <SettingsGroup :title="$t('qwdtt.share')" :desc="$t('qwdtt.shareDesc')">
      <template v-if="link">
        <div class="share-grid">
          <div class="qr-box"><QrcodeVue :value="link" :size="180" level="M" render-as="svg" background="#ffffff" foreground="#000000" /></div>
          <div style="flex: 1; min-width: 0;">
            <textarea class="input mono" rows="5" readonly :value="link" @focus="($event.target as HTMLTextAreaElement).select()" />
            <div class="btn-row">
              <Btn sm @click="copyToClipboard(link)"><Ico name="copy" :size="15" /> {{ $t('copyToClipboard') }}</Btn>
              <Btn sm @click="showSubscription"><Ico name="list" :size="15" /> {{ $t('qwdtt.subscriptionJson') }}</Btn>
            </div>
          </div>
        </div>
        <textarea v-if="subscriptionDoc" class="input mono" rows="12" readonly :value="subscriptionDoc" style="margin-top: 12px;" />
      </template>
      <div v-else class="fieldnote"><span>{{ $t('qwdtt.noLink') }}</span></div>
    </SettingsGroup>

    <!-- ===================== log ===================== -->
    <SettingsGroup :title="$t('qwdtt.log')">
      <div class="btn-row" style="margin-top: 0;">
        <Btn sm :loading="logLoading" @click="loadLogs"><Ico name="refresh" :size="15" /> {{ $t('qwdtt.refresh') }}</Btn>
      </div>
      <pre v-if="logs.length" class="log-box">{{ logs.join('\n') }}</pre>
      <div v-else class="fieldnote" style="margin-top: 10px;"><span>{{ $t('qwdtt.noLogs') }}</span></div>
    </SettingsGroup>
  </div>
</template>

<script lang="ts" setup>
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import QrcodeVue from 'qrcode.vue'
import Data from '@/store/modules/data'
import HttpUtils from '@/plugins/httputil'
import { copyToClipboard } from '@/plugins/clipboard'
import Btn from '@/components/ui/Btn.vue'
import Ico from '@/components/ui/Ico.vue'
import Chip from '@/components/ui/Chip.vue'
import SettingsGroup from '@/components/ui/SettingsGroup.vue'

interface QwdttConfig {
  remark: string
  enabled: boolean
  listenAddr: string
  wgPort: number
  password: string
  dns: string
  configDir: string
  listenRaw: string
  listenDirect: string
  subHost: string
  vkHashes: string
  clientPort: number
  workers: number
  autoVkHash: boolean
  routeThroughCore: boolean
  outboundTag: string
  userMode: string
  maxDevices: number
}

interface QwdttStatus {
  supported: boolean
  enabled: boolean
  running: boolean
  pid: number
  uptime: number
  binary: string
  binaryFound: boolean
  routed: boolean
  tunReady: boolean
  error?: string
  lastExit?: string
  hashEnv: boolean
  hasCookies: boolean
  hasRemote?: boolean
}

interface HashCheck {
  hash: string
  alive: boolean | null
  generated: boolean
  error?: string
}

const data = Data()

const blank = (): QwdttConfig => ({
  remark: '', enabled: false, listenAddr: '0.0.0.0:56000', wgPort: 56001, password: '', dns: '8.8.8.8',
  configDir: '', listenRaw: '0.0.0.0:56003', listenDirect: '', subHost: '', vkHashes: '', clientPort: 9000,
  workers: 16, autoVkHash: true, routeThroughCore: true, outboundTag: '', userMode: 'perClient', maxDevices: 3,
})

const cfg = ref<QwdttConfig>(blank())
const inboundId = ref(0)
const status = ref<QwdttStatus>({
  supported: true, enabled: false, running: false, pid: 0, uptime: 0, binary: '', binaryFound: true,
  routed: false, tunReady: false, hashEnv: false, hasCookies: false,
})
const link = ref('')
const resolvedHost = ref('')
const hashChecks = ref<HashCheck[]>([])
const hashBusy = ref<Record<string, boolean>>({})
const logs = ref<string[]>([])
const subscriptionDoc = ref('')
const cookiesText = ref('')
const cookiesOk = ref<boolean | null>(null)
const cookiesErr = ref('')

const remote = ref<any>({ url: '', username: '', hasPassword: false, hasToken: false, fromEnv: false })
const remoteForm = ref({ url: '', username: '', password: '', token: '' })
const remoteBusy = ref(false)
const users = ref<any[]>([])
const usersLoading = ref(false)
const nodeInbounds = ref(0)

const restarting = ref(false)
const generating = ref(false)
const checking = ref(false)
const cookieBusy = ref(false)
const logLoading = ref(false)

const hashList = computed((): string[] => cfg.value.vkHashes.split(/[,;\s]+/).filter(Boolean))
const hashCount = computed(() => hashList.value.length)
// A hash can be made with the panel's own VK cookies or through a WDTT panel.
const canGen = computed(() => status.value.hasCookies || !!status.value.hasRemote)

const bytes = (n: number): string => {
  if (!n) return '0'
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++ }
  return n.toFixed(i ? 1 : 0) + ' ' + u[i]
}

const uptime = computed(() => {
  const s = status.value.uptime
  if (s < 60) return s + 's'
  if (s < 3600) return Math.floor(s / 60) + 'm'
  if (s < 86400) return Math.floor(s / 3600) + 'h'
  return Math.floor(s / 86400) + 'd'
})

// Apply a server view. The settings are read-only here (they live in the
// inbound); only what the page itself changes -- hashes, cookies, status -- is
// refreshed.
const apply = (view: any) => {
  if (!view) return
  status.value = view.status
  link.value = view.link ?? ''
  resolvedHost.value = view.resolvedHost ?? ''
  inboundId.value = view.inboundId ?? 0
  nodeInbounds.value = view.nodeInbounds ?? 0
  if (view.config) cfg.value = { ...blank(), ...view.config }
  if (view.remote) {
    const first = !remote.value.url && !remoteForm.value.url
    remote.value = view.remote
    if (first) remoteForm.value = { url: view.remote.url ?? '', username: view.remote.username ?? '', password: '', token: '' }
  }
}

const saveRemote = async (clear: boolean) => {
  remoteBusy.value = true
  const body = clear ? { url: '' } : remoteForm.value
  const msg = await HttpUtils.post('api/qwdttRemote', body)
  remoteBusy.value = false
  if (msg.success) {
    remoteForm.value.password = ''
    remoteForm.value.token = ''
    if (clear) remoteForm.value = { url: '', username: '', password: '', token: '' }
    apply(msg.obj)
  }
}

const loadUsers = async () => {
  usersLoading.value = true
  const msg = await HttpUtils.get('api/qwdttUsers')
  usersLoading.value = false
  if (msg.success) users.value = msg.obj ?? []
}

const loadCookies = async () => {
  const msg = await HttpUtils.get('api/qwdttCookiesGet')
  if (msg.success && msg.obj?.cookies) {
    cookiesText.value = msg.obj.cookies
  }
}

const load = async () => {
  const msg = await HttpUtils.get('api/qwdtt')
  if (msg.success) apply(msg.obj)
  await loadCookies()
}

const restart = async () => {
  restarting.value = true
  const msg = await HttpUtils.post('api/qwdttRestart', {})
  restarting.value = false
  if (msg.success) apply(msg.obj)
}

const generate = async (replace: boolean) => {
  generating.value = true
  const msg = await HttpUtils.post('api/qwdttGenerate', { replace })
  generating.value = false
  if (msg.success) {
    apply(msg.obj)
    hashChecks.value = []
    await checkHashes()
  }
}

const endCall = async (hash: string) => {
  hashBusy.value[hash] = true
  const msg = await HttpUtils.post('api/qwdttEndCall', { hash })
  hashBusy.value[hash] = false
  if (msg.success) {
    apply(msg.obj)
    hashChecks.value = []
  }
}

const deleteHash = async (hash: string) => {
  hashBusy.value[hash] = true
  const msg = await HttpUtils.post('api/qwdttDeleteHash', { hash })
  hashBusy.value[hash] = false
  if (msg.success) {
    apply(msg.obj)
    hashChecks.value = []
  }
}

const checkHashes = async () => {
  checking.value = true
  const msg = await HttpUtils.post('api/qwdttHashesCheck', {})
  checking.value = false
  if (msg.success) hashChecks.value = msg.obj ?? []
}

const saveCookies = async () => {
  cookieBusy.value = true
  const msg = await HttpUtils.post('api/qwdttCookies', { cookies: cookiesText.value })
  cookieBusy.value = false
  if (msg.success) {
    cookiesOk.value = null
    apply(msg.obj)
    await checkCookies()
  }
}

const checkCookies = async () => {
  cookieBusy.value = true
  const msg = await HttpUtils.post('api/qwdttCookiesCheck', {})
  cookieBusy.value = false
  cookiesOk.value = msg.success
  cookiesErr.value = msg.success ? '' : msg.msg
}

const clearCookies = async () => {
  cookieBusy.value = true
  const msg = await HttpUtils.post('api/qwdttCookiesClear', {})
  cookieBusy.value = false
  if (msg.success) {
    cookiesOk.value = null
    apply(msg.obj)
  }
}

const showSubscription = async () => {
  const msg = await HttpUtils.get('api/qwdttSubscription')
  if (msg.success) subscriptionDoc.value = msg.obj ?? ''
}

const loadLogs = async () => {
  logLoading.value = true
  const msg = await HttpUtils.get('api/qwdttLogs', { c: 200 })
  logLoading.value = false
  if (msg.success) logs.value = msg.obj ?? []
}

let timer: ReturnType<typeof setInterval> | undefined
onMounted(async () => {
  await load()
  loadLogs()
  loadUsers()
  timer = setInterval(() => load(), 10000)
})
onBeforeUnmount(() => clearInterval(timer))
</script>

<style scoped>
.head-row { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; }
.head-actions { display: flex; gap: 8px; }
.btn-row { display: flex; gap: 8px; flex-wrap: wrap; margin-top: 12px; }
.fieldnote { font-size: 12.5px; color: var(--text-3); padding: 8px 12px; border-radius: 10px; background: var(--surface-3); }
.fieldnote.warn { color: var(--amber); }
.fieldnote.ok { color: var(--emerald); }
.hash-list { display: flex; flex-direction: column; gap: 6px; margin-top: 12px; }
.hash-row { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.hash-name { font-size: 13px; }
.grid2 { display: grid; grid-template-columns: 1fr 1fr; gap: 8px; }
@media (max-width: 640px) { .grid2 { grid-template-columns: 1fr; } }
table.users { width: 100%; border-collapse: collapse; font-size: 13px; }
table.users th, table.users td { text-align: left; padding: 6px 8px; border-bottom: 1px solid var(--border, rgba(127,127,127,.15)); }
table.users th { color: var(--text-3); font-weight: 500; }
.share-grid { display: flex; gap: 18px; align-items: flex-start; flex-wrap: wrap; }
.qr-box { background: #fff; padding: 10px; border-radius: 12px; line-height: 0; }
.log-box {
  margin: 10px 0 0; padding: 12px; border-radius: 10px; background: var(--surface-3);
  max-height: 320px; overflow: auto; font-family: var(--font-mono); font-size: 12px; white-space: pre-wrap; word-break: break-all;
}
textarea.input { width: 100%; height: auto; resize: vertical; font-family: var(--font-mono); }
</style>
