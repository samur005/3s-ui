<template>
  <Field label="Listen Port">
    <input class="input mono" type="number" min="1" max="65535" v-model.number="data.listen_port" />
  </Field>
  <Field label="Credentials (user:pass, one per line)">
    <textarea class="input mono" rows="4" v-model="data.credentials" placeholder="user1:pass1&#10;user2:pass2" />
  </Field>
  <MHint>Requires trusttunnel_endpoint binary. Process management will be added in a follow-up.</MHint>
</template>

<script lang="ts" setup>
import Field from '@/components/ui/Field.vue'
import MHint from '@/components/ui/MHint.vue'

defineProps<{ data: any; isNew?: boolean }>()
</script>
