<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { mdiMagnify } from '@mdi/js'

/** KB search input: emits the query 300 ms after typing stops (FE-051). Not a form, no rules needed. */
const props = defineProps<{ modelValue: string; autofocus?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [string] }>()
const text = ref(props.modelValue)
let timer: ReturnType<typeof setTimeout> | null = null
watch(
  () => props.modelValue,
  (v) => {
    if (v !== text.value.trim()) text.value = v
  },
)
watch(text, (v) => {
  if (timer) clearTimeout(timer)
  timer = setTimeout(() => emit('update:modelValue', (v ?? '').trim()), 300)
})
onBeforeUnmount(() => timer && clearTimeout(timer))
</script>

<template>
  <v-text-field
    v-model="text"
    :label="$t('kb.searchLabel')"
    :placeholder="$t('kb.searchPlaceholder')"
    :prepend-inner-icon="mdiMagnify"
    :autofocus="autofocus"
    clearable
    hide-details
    type="search"
    data-testid="kb-search"
    @click:clear="text = ''"
  />
</template>
