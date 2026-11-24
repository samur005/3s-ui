<template>
  <div class="view-toggle" role="group" :aria-label="$t('ui.view')">
    <button
      type="button"
      class="vt-btn"
      :class="{ on: modelValue === 'cards' }"
      :title="$t('ui.viewCards')"
      @click="$emit('update:modelValue', 'cards')"
    >
      <Ico name="basics" :size="14" />
      <span class="vt-label">{{ $t('ui.viewCards') }}</span>
    </button>
    <button
      type="button"
      class="vt-btn"
      :class="{ on: modelValue === 'list' }"
      :title="$t('ui.viewList')"
      @click="$emit('update:modelValue', 'list')"
    >
      <Ico name="list" :size="14" />
      <span class="vt-label">{{ $t('ui.viewList') }}</span>
    </button>
  </div>
</template>

<script setup lang="ts">
defineProps<{ modelValue: 'cards' | 'list' }>()
defineEmits<{ 'update:modelValue': [v: 'cards' | 'list'] }>()
</script>

<style scoped>
.view-toggle {
  display: inline-flex;
  border: 1px solid var(--border);
  border-radius: 8px;
  overflow: hidden;
  background: var(--bg-2);
}
.vt-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 5px;
  height: 30px;
  padding: 0 10px;
  border: 0;
  background: transparent;
  color: var(--text-3);
  cursor: pointer;
  font-size: 12.5px;
  font-weight: 500;
}
.vt-label { white-space: nowrap; }
.vt-btn.on {
  background: var(--surface);
  color: var(--text);
  box-shadow: var(--shadow-1);
}
.vt-btn:hover { color: var(--text-1); }
</style>
