<template>
  <div>
    <SectionLabel style="margin-bottom: 12px;">Xray VLESS</SectionLabel>
    <MHint style="margin-bottom: 14px;">{{ $t('xray.vlessDesc') }}</MHint>

    <div v-if="status && status.binaryFound === false" class="note warn">{{ $t('xray.binaryMissing', { path: status.binary || 'xray' }) }}</div>
    <div v-else-if="status && status.error" class="note warn">{{ status.error }}</div>
    <div v-if="status && status.running" style="margin-bottom: 12px; display: flex; gap: 8px; flex-wrap: wrap;">
      <Chip color="emerald" dot>{{ $t('xray.running') }}</Chip>
      <Chip>{{ status.version || status.pinned }}</Chip>
    </div>

    <div class="grid2">
      <Field :label="$t('xray.port')">
        <input class="input mono" type="number" min="1" max="65535" v-model.number="data.listen_port" />
      </Field>
      <Field :label="$t('xray.listen')">
        <input class="input mono" v-model="data.listen" placeholder="0.0.0.0" />
      </Field>
    </div>
    <Field :label="$t('xray.endpoint')" :hint="$t('xray.endpointHint')">
      <input class="input mono" v-model="data.endpoint" placeholder="vpn.example.com" />
    </Field>

    <div class="grid2">
      <Field :label="$t('xray.method')" :hint="$t('xray.methodHint')">
        <Select v-model="data.method">
          <option v-for="m in XvlessMethods" :key="m" :value="m">{{ m }}</option>
        </Select>
      </Field>
      <Field :label="$t('xray.security')">
        <Select v-model="data.security">
          <option v-for="s in XvlessSecurities" :key="s" :value="s">{{ s }}</option>
        </Select>
      </Field>
    </div>

    <template v-if="data.method === 'xhttp' || data.method === 'websocket' || data.method === 'httpupgrade'">
      <div class="grid2">
        <Field label="path"><input class="input mono" v-model="data.path" placeholder="/" /></Field>
        <Field label="host"><input class="input mono" v-model="data.host" /></Field>
      </div>
      <Field v-if="data.method === 'xhttp'" :label="$t('xray.xhttpMode')">
        <Select v-model="data.xhttpMode">
          <option v-for="m in XhttpModes" :key="m" :value="m">{{ m }}</option>
        </Select>
      </Field>
    </template>
    <Field v-if="data.method === 'grpc'" label="serviceName">
      <input class="input mono" v-model="data.serviceName" />
    </Field>
    <template v-if="data.method === 'mkcp'">
      <MHint style="margin-bottom: 12px;">{{ $t('xray.mkcpHint') }}</MHint>
      <div class="grid2">
        <Field label="mtu"><input class="input mono" type="number" v-model.number="data.kcpMtu" /></Field>
        <Field label="tti"><input class="input mono" type="number" v-model.number="data.kcpTti" /></Field>
        <Field label="uplinkCapacity"><input class="input mono" type="number" v-model.number="data.kcpUplinkCapacity" /></Field>
        <Field label="downlinkCapacity"><input class="input mono" type="number" v-model.number="data.kcpDownlinkCapacity" /></Field>
      </div>
      <div class="grid2">
        <Field :label="$t('xray.finalMaskType')" :hint="$t('xray.finalMaskHint')">
          <input class="input mono" v-model="data.finalMaskType" placeholder="salamander" />
        </Field>
        <Field label="password"><input class="input mono" v-model="data.finalMaskPassword" /></Field>
      </div>
    </template>

    <template v-if="data.security === 'tls'">
      <div class="grid2">
        <Field :label="$t('xray.certFile')"><input class="input mono" v-model="data.certFile" /></Field>
        <Field :label="$t('xray.keyFile')"><input class="input mono" v-model="data.keyFile" /></Field>
      </div>
      <Field label="SNI"><input class="input mono" v-model="data.serverName" /></Field>
    </template>

    <template v-if="data.security === 'reality'">
      <Field :label="$t('xray.realityDest')" :hint="$t('xray.realityDestHint')">
        <input class="input mono" v-model="data.realityDest" placeholder="www.cloudflare.com:443" />
      </Field>
      <Field :label="$t('xray.realityServerNames')">
        <input class="input mono" :value="(data.realityServerNames||[]).join(',')" @input="onNames" />
      </Field>
      <div class="grid2">
        <Field :label="$t('xray.realityPrivateKey')">
          <div style="display:flex; gap:6px;">
            <input class="input mono" v-model="data.realityPrivateKey" style="flex:1;" />
            <Btn sm :loading="genKey" @click="generateRealityKey"><Ico name="key" :size="14" /></Btn>
          </div>
        </Field>
        <Field :label="$t('xray.realityPublicKey')" :hint="$t('xray.realityPublicKeyHint')"><input class="input mono" v-model="data.realityPublicKey" /></Field>
      </div>
      <div class="grid2">
        <Field label="shortIds">
          <div style="display:flex; gap:6px;">
            <input class="input mono" :value="(data.realityShortIds||[]).join(',')" @input="onShortIds" style="flex:1;" />
            <Btn sm @click="generateShortId"><Ico name="refresh" :size="14" /></Btn>
          </div>
        </Field>
        <Field label="fingerprint"><input class="input mono" v-model="data.realityFingerprint" placeholder="chrome" /></Field>
      </div>
    </template>

    <hr class="form-divider" />
    <SectionLabel style="margin-bottom: 12px;">{{ $t('xray.encryption') }}</SectionLabel>
    <MHint style="margin-bottom: 12px;">{{ $t('xray.encryptionDesc') }}</MHint>
    <div style="display: flex; gap: 8px; margin-bottom: 12px;">
      <Btn sm :loading="generating" @click="genEnc"><Ico name="refresh" :size="14" /> {{ $t('xray.generateEnc') }}</Btn>
    </div>
    <Field label="decryption (server)"><input class="input mono" v-model="data.decryption" placeholder="none" /></Field>
    <Field label="encryption (client)"><input class="input mono" v-model="data.encryption" /></Field>
    <Field :label="$t('xray.flow')" :hint="$t('xray.flowHint')">
      <Select v-model="data.flow">
        <option value="">{{ $t('ui.none') }}</option>
        <option value="xtls-rprx-vision">xtls-rprx-vision</option>
      </Select>
    </Field>

    <hr class="form-divider" />
    <SectionLabel style="margin-bottom: 12px;">{{ $t('xray.routing') }}</SectionLabel>
    <div style="margin-bottom: 12px;">
      <SwitchLabel v-model="data.routeThroughCore" :label="$t('xray.routeThroughCore')" />
      <MHint style="margin-top: 4px;">{{ $t('xray.routeThroughCoreDesc') }}</MHint>
    </div>
    <Field v-if="data.routeThroughCore" :label="$t('xray.outboundTag')" :hint="$t('xray.outboundTagHint')">
      <Select v-model="data.outboundTag">
        <option value="">{{ $t('xray.byRules') }}</option>
        <option v-for="o in outboundTags" :key="o" :value="o">{{ o }}</option>
      </Select>
    </Field>
    <MHint>{{ $t('xray.handoffLimitations') }}</MHint>
  </div>
</template>

<script lang="ts" setup>
import { computed, onMounted, ref } from 'vue'
import Data from '@/store/modules/data'
import HttpUtils from '@/plugins/httputil'
import { XvlessMethods, XvlessSecurities, XhttpModes } from '@/types/inbounds'
import Field from '@/components/ui/Field.vue'
import Select from '@/components/ui/Select.vue'
import SectionLabel from '@/components/ui/SectionLabel.vue'
import SwitchLabel from '@/components/ui/SwitchLabel.vue'
import MHint from '@/components/ui/MHint.vue'
import Chip from '@/components/ui/Chip.vue'
import Btn from '@/components/ui/Btn.vue'
import Ico from '@/components/ui/Ico.vue'

const props = defineProps<{ data: any; isNew: boolean }>()
const status = ref<any>(null)
const generating = ref(false)
const outboundTags = computed((): string[] =>
  ((Data().outbounds ?? []) as any[]).map((o) => o.tag as string).filter(Boolean),
)

const loadStatus = async () => {
  const msg = await HttpUtils.get('api/xrayStatus')
  if (msg.success) status.value = msg.obj
}
const genKey = ref(false)
const genCert = ref(false)

const usePanelCert = async () => {
  const msg = await HttpUtils.get('api/certs')
  if (msg?.success && msg.obj?.length) {
    const c = msg.obj[0]
    data.certFile = c.certFile || ''
    data.keyFile = c.keyFile || ''
  }
}

const generateSelfSigned = async () => {
  genCert.value = true
  const msg = await HttpUtils.post('api/generateSelfSigned', { domain: data.serverName || data.endpoint || 'localhost' })
  genCert.value = false
  if (msg?.success && msg.obj) {
    data.certFile = msg.obj.certFile || ''
    data.keyFile = msg.obj.keyFile || ''
  }
}

const genKey = ref(false)
const generateRealityKey = async () => {
  genKey.value = true
  const msg = await HttpUtils.post('api/generateRealityKey', {})
  genKey.value = false
  if (msg?.success && msg.obj) {
    data.realityPrivateKey = msg.obj.privateKey || ''
    data.realityPublicKey = msg.obj.publicKey || ''
  }
}
const generateShortId = () => {
  const sid = Math.random().toString(16).slice(2, 10)
  const cur = data.realityShortIds || []
  data.realityShortIds = [...cur, sid]
}

const genEnc = async () => {
  generating.value = true
  try {
    const msg = await HttpUtils.get('api/xrayVlessEnc')
    if (msg.success && msg.obj) {
      props.data.decryption = msg.obj.decryption
      props.data.encryption = msg.obj.encryption
    }
  } finally {
    generating.value = false
  }
}
const onNames = (e: Event) => {
  props.data.realityServerNames = (e.target as HTMLInputElement).value.split(',').map((s) => s.trim()).filter(Boolean)
}
const onShortIds = (e: Event) => {
  props.data.realityShortIds = (e.target as HTMLInputElement).value.split(',').map((s) => s.trim())
}
onMounted(loadStatus)
</script>

<style scoped>
.note { font-size: 12.5px; padding: 8px 12px; border-radius: 10px; background: var(--surface-3); margin-bottom: 12px; color: var(--text-3); }
.note.warn { color: var(--amber); }
</style>
