<template>
  <Wg :data="data" :is-new="isNew" is-awg>
    <template #obfuscation>
      <hr class="form-divider" />
      <div style="display: flex; align-items: center; gap: 10px; margin-bottom: 12px;">
        <SectionLabel style="flex: 1;">{{ $t('wg.obfuscation') }}</SectionLabel>
        <Btn sm :loading="generating" @click="regenerate">
          <Ico name="refresh" :size="14" /> {{ $t('wg.generate') }}
        </Btn>
      </div>
      <Field :label="$t('wg.version')" :hint="$t('wg.versionHint')">
        <Select v-model="data.version" @change="versionChanged">
          <option v-for="v in AwgVersions" :key="v" :value="v">AmneziaWG {{ v }}</option>
        </Select>
      </Field>
      <MHint v-if="data.version === '2.5'" style="margin-bottom: 12px;">{{ $t('wg.v25Note') }}</MHint>

      <div class="grid3">
        <Field label="Jc"><input class="input mono" type="number" min="0" max="128" v-model.number="data.jc" /></Field>
        <Field label="Jmin"><input class="input mono" type="number" min="0" v-model.number="data.jmin" /></Field>
        <Field label="Jmax"><input class="input mono" type="number" min="0" v-model.number="data.jmax" /></Field>
      </div>
      <div :class="fields.s34 ? 'grid4' : 'grid2'">
        <Field label="S1"><input class="input mono" type="number" min="0" v-model.number="data.s1" /></Field>
        <Field label="S2"><input class="input mono" type="number" min="0" v-model.number="data.s2" /></Field>
        <Field v-if="fields.s34" label="S3"><input class="input mono" type="number" min="0" v-model.number="data.s3" /></Field>
        <Field v-if="fields.s34" label="S4"><input class="input mono" type="number" min="0" v-model.number="data.s4" /></Field>
      </div>
      <MHint v-if="fields.protection" style="margin-bottom: 12px;">{{ $t('wg.s12Min') }}</MHint>
      <div class="grid2">
        <Field v-for="n in 4" :key="n" :label="'H' + n" :hint="n === 1 ? $t(fields.hRange ? 'wg.hRangeHint' : 'wg.hSingleHint') : ''">
          <input class="input mono" v-model="data['h' + n]" :placeholder="fields.hRange ? '100000-200000' : '123456789'" />
        </Field>
      </div>

      <template v-if="fields.cps">
        <Field v-for="n in 5" :key="'i' + n" :label="'I' + n" :hint="n === 1 ? $t('wg.cpsHint') : ''">
          <input class="input mono" v-model="data['i' + n]" placeholder="<b 0x…><r 32><t>" />
        </Field>
      </template>

      <template v-if="fields.protection">
        <hr class="form-divider" />
        <SectionLabel style="margin-bottom: 12px;">{{ $t('wg.protection') }}</SectionLabel>
        <Field :label="$t('wg.headerProtectionKey')" :hint="$t('wg.headerProtectionKeyHint')">
          <input class="input mono" v-model="data.headerProtectionKey" />
        </Field>
        <div class="grid2">
          <Field :label="$t('wg.contentPaddingAddition')" :hint="$t('wg.contentPaddingHint')">
            <input class="input mono" v-model="data.contentPaddingAddition" placeholder="0-64" />
          </Field>
          <Field :label="$t('wg.maxHandshakeAttempts')">
            <input class="input mono" v-model="data.maxHandshakeAttempts" placeholder="20" />
          </Field>
          <Field :label="$t('wg.rekeyAfterTime')" :hint="$t('wg.rangeHint')">
            <input class="input mono" v-model="data.rekeyAfterTime" placeholder="120-130" />
          </Field>
          <Field :label="$t('wg.rekeyTimeout')">
            <input class="input mono" v-model="data.rekeyTimeout" placeholder="5-6" />
          </Field>
          <Field :label="$t('wg.rejectAfterTime')">
            <input class="input mono" v-model="data.rejectAfterTime" placeholder="180-190" />
          </Field>
          <Field :label="$t('wg.keepaliveTimeout')">
            <input class="input mono" v-model="data.keepaliveTimeout" placeholder="10-12" />
          </Field>
        </div>
      </template>

      <template v-if="fields.trailersCookie">
        <div style="margin-bottom: 12px;">
          <SwitchLabel v-model="data.randomTrailers" :label="$t('wg.randomTrailers')" />
          <MHint style="margin-top: 4px;">{{ $t('wg.randomTrailersDesc') }}</MHint>
        </div>
        <div style="margin-bottom: 12px;">
          <SwitchLabel v-model="data.disableCookies" :label="$t('wg.disableCookies')" />
          <MHint style="margin-top: 4px;">{{ $t('wg.disableCookiesDesc') }}</MHint>
        </div>
      </template>
    </template>
  </Wg>
</template>

<script lang="ts" setup>
import { computed, onMounted, ref } from 'vue'
import HttpUtils from '@/plugins/httputil'
import { AwgVersions, AwgObfuscationKeys, awgFields } from '@/types/inbounds'
import Wg from '@/components/forms/in/Wg.vue'
import Field from '@/components/ui/Field.vue'
import Select from '@/components/ui/Select.vue'
import SectionLabel from '@/components/ui/SectionLabel.vue'
import SwitchLabel from '@/components/ui/SwitchLabel.vue'
import MHint from '@/components/ui/MHint.vue'
import Btn from '@/components/ui/Btn.vue'
import Ico from '@/components/ui/Ico.vue'

const props = defineProps<{ data: any; isNew: boolean }>()

const generating = ref(false)
const fields = computed(() => awgFields(props.data.version))

const empty = (v: any): boolean => v === '' || v === 0 || v === undefined || v === null || v === false

// replace=true is the button: a whole new parameter set. A version switch only
// tops up what the new version needs and the form has not got, so values the
// operator typed (and the clients already hold) stay.
const lastVersion = ref<string>(props.data.version)
const HeaderKeys = ['h1', 'h2', 'h3', 'h4', 's1', 's2', 's3', 's4']
const fill = async (replace: boolean) => {
  generating.value = true
  // Moving between header styles (single numbers, ranges) or into 3.x, where
  // S1-S4 must be 12 or more, makes the old values unusable: take new ones.
  const before = awgFields(lastVersion.value)
  const after = fields.value
  const restyle = before.hRange !== after.hRange || (after.protection && !before.protection)
  lastVersion.value = props.data.version
  try {
    const msg = await HttpUtils.get('api/wgGenerate', { version: props.data.version })
    if (!msg.success || !msg.obj) return
    for (const k of AwgObfuscationKeys) {
      if (!(k in msg.obj)) continue
      if (replace || empty(props.data[k]) || (restyle && HeaderKeys.includes(k))) props.data[k] = msg.obj[k]
    }
  } finally {
    generating.value = false
  }
}

const regenerate = () => fill(true)
const versionChanged = () => fill(false)

onMounted(() => {
  if (props.isNew) fill(false)
})
</script>

<style scoped>
.grid3 { display: grid; grid-template-columns: repeat(3, 1fr); gap: 12px; }
.grid4 { display: grid; grid-template-columns: repeat(4, 1fr); gap: 12px; }
</style>
