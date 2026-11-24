<template>
  <span
    class="status-dot"
    :class="{ on: online }"
    :title="online ? $t('ui.online') : $t('ui.offline')"
  />
</template>

<script setup lang="ts">
defineProps<{ online: boolean }>()
</script>

<style scoped>
.status-dot {
  display: inline-block;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--text-3);
  flex: none;
  vertical-align: middle;
}
.status-dot.on {
  background: var(--emerald);
  box-shadow: 0 0 0 2px color-mix(in srgb, var(--emerald) 25%, transparent);
}
</style>
