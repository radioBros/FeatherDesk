<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** The recovery codes, shown once, with copy and download (FE-070/FE-072). */
import { ref } from 'vue'
import { mdiContentCopy, mdiDownload } from '@mdi/js'
import { useConfigStore } from '@/stores/config'
import { downloadRecoveryCodes } from './mfa'

const props = defineProps<{ codes: string[] }>()
const config = useConfigStore()
const copied = ref(false)

async function copy() {
  try {
    await navigator.clipboard.writeText(props.codes.join('\n'))
    copied.value = true
  } catch {
    copied.value = false
  }
}
function download() {
  downloadRecoveryCodes(props.codes, config.branding.product_name || 'SimpleTickets')
}
</script>

<template>
  <div data-testid="mfa-recovery-codes">
    <v-alert type="warning" variant="tonal" density="compact" class="mb-3">{{ $t('mfa.codes.warning') }}</v-alert>
    <ul class="mfa-codes text-body-1 mb-3" :aria-label="$t('mfa.codes.title')">
      <li v-for="c in codes" :key="c" data-testid="mfa-recovery-code"><code>{{ c }}</code></li>
    </ul>
    <div class="d-flex flex-wrap ga-2">
      <v-btn variant="tonal" :prepend-icon="mdiContentCopy" data-testid="mfa-codes-copy" @click="copy">{{ copied ? $t('mfa.codes.copied') : $t('mfa.codes.copy') }}</v-btn>
      <v-btn variant="tonal" :prepend-icon="mdiDownload" data-testid="mfa-codes-download" @click="download">{{ $t('mfa.codes.download') }}</v-btn>
    </div>
  </div>
</template>

<style scoped>
.mfa-codes {
  list-style: none;
  padding: 0;
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 4px 16px;
  font-family: ui-monospace, monospace;
}
</style>
