<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** Security card of /agent/admin/settings (FE-074): loads GET /admin/security, edits it through SecurityPolicyForm. */
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, unwrap } from '@/api/client'
import { useUiStore } from '@/stores/ui'
import SecurityPolicyForm from './SecurityPolicyForm.vue'
import type { components } from '@/api/schema'

const { t } = useI18n()
const ui = useUiStore()
const settings = ref<components['schemas']['SecuritySettings'] | null>(null)
const failed = ref(false)
const key = ref(0)

onMounted(async () => {
  try {
    settings.value = await unwrap(api.GET('/admin/security'))
    key.value++
  } catch {
    failed.value = true
  }
})
function onSaved(s: components['schemas']['SecuritySettings']) {
  settings.value = s
  ui.notify(t('mfa.policy.saved'), 'success')
}
</script>

<template>
  <v-card border flat class="pa-4 mt-4" :title="$t('mfa.policy.title')" data-testid="security-card">
    <v-alert v-if="failed" type="error" variant="tonal" density="compact">{{ $t('mfa.profile.loadFailed') }}</v-alert>
    <v-progress-linear v-else-if="!settings" indeterminate />
    <SecurityPolicyForm v-else :key="key" :settings="settings" @saved="onSaved" />
  </v-card>
</template>
