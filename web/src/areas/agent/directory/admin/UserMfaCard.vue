<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Admin view of a user's two-factor authentication (FE-073, D-19 §B): status
 * and "Reset two-factor" (lost device). Shown in the staff edit dialog and on
 * the customer detail page; renders nothing for non-admins.
 */
import { onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, unwrap } from '@/api/client'
import { ApiProblem } from '@/api/problem'
import { useSessionStore } from '@/stores/session'
import { useUiStore } from '@/stores/ui'
import { useConfirm } from '@/composables/useConfirm'
import type { components } from '@/api/schema'

const props = defineProps<{ userId: string }>()
const { t } = useI18n()
const session = useSessionStore()
const ui = useUiStore()
const { confirm } = useConfirm()
const status = ref<components['schemas']['AdminMfaStatus'] | null>(null)
const busy = ref(false)

async function load() {
  if (!session.isAdmin || !props.userId) return
  try {
    status.value = await unwrap(api.GET('/admin/users/{id}/mfa', { params: { path: { id: props.userId } } }))
  } catch {
    status.value = null
  }
}
onMounted(load)
watch(() => props.userId, load)

async function reset() {
  if (!(await confirm({ title: t('mfa.admin.resetTitle'), body: t('mfa.admin.resetConfirm'), color: 'error' }))) return
  busy.value = true
  try {
    await unwrap(api.POST('/admin/users/{id}/mfa/reset', { params: { path: { id: props.userId } } }))
    ui.notify(t('mfa.admin.resetDone'), 'success')
    await load()
  } catch (e) {
    ui.notify(e instanceof ApiProblem ? e.userMessage : t('errors.generic'), 'error')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <v-card v-if="session.isAdmin && status" border flat class="pa-3 mt-4 d-flex align-center flex-wrap ga-2" data-testid="user-mfa">
    <span class="text-body-2">
      {{ $t('mfa.admin.label') }}:
      <strong data-testid="user-mfa-state">{{ !status.applies ? $t('mfa.admin.sso') : status.enabled ? $t('mfa.profile.on') : $t('mfa.profile.off') }}</strong>
    </span>
    <v-spacer />
    <v-btn
      v-if="status.applies"
      size="small"
      variant="tonal"
      color="error"
      :disabled="!status.enabled"
      :loading="busy"
      data-testid="user-mfa-reset"
      @click="reset"
    >
      {{ $t('mfa.admin.reset') }}
    </v-btn>
  </v-card>
</template>
