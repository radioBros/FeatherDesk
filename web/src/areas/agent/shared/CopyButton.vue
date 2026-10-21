<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { ref } from 'vue'
import { mdiCheck, mdiContentCopy } from '@mdi/js'

const props = defineProps<{ value: string }>()
const copied = ref(false)
async function copy() {
  try {
    await navigator.clipboard.writeText(props.value)
    copied.value = true
    setTimeout(() => (copied.value = false), 2000)
  } catch {
    /* clipboard unavailable (http, permissions): the value stays selectable */
  }
}
</script>

<template>
  <v-btn
    :icon="copied ? mdiCheck : mdiContentCopy"
    variant="text"
    size="small"
    :aria-label="copied ? $t('agent.common.copied') : $t('agent.common.copy')"
    data-testid="copy-button"
    @click="copy"
  />
</template>
