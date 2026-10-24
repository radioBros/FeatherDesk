<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * The type × channel matrix (D-19 §D, FE-095) for `PATCH /me`
 * `notification_prefs`, plus the browser permission prompt and the optional
 * sound (FE-094). The page passes the current matrix in; the form never loads
 * data. A browser channel switched on needs the browser's permission
 * (validations.browserNotifications), so the save is blocked until it is
 * granted; `customer.reply` email is always on (the API answers not_optional).
 */
import { computed, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import { useSessionStore } from '@/stores/session'
import type { Me } from '@/types'
import * as browser from './browser'
import { EMAIL_FORCED, typeKey, typesFor, type Area, type Channels, type Prefs } from './types'

const props = defineProps<{ area: Area; prefs?: Prefs; permission?: browser.Permission }>()
const emit = defineEmits<{ saved: [] }>()
const { t } = useI18n()
const session = useSessionStore()
const types = computed(() => typesFor(props.area))
const model = reactive<Record<string, Channels>>(
  Object.fromEntries(
    typesFor(props.area).map((k) => {
      const c = props.prefs?.[k]
      return [k, { in_app: c?.in_app ?? true, email: EMAIL_FORCED.includes(k) || (c?.email ?? true), browser: c?.browser ?? false }]
    }),
  ),
)
const permission = ref<browser.Permission>(props.permission ?? browser.permission())
const sound = ref(browser.soundEnabled())
const browserRules = [validations.browserNotifications(() => permission.value)]
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

async function enable() {
  permission.value = await browser.requestPermission()
  await formRef.value?.validate()
}

const onSave = submit(async () => {
  // Browser pop-ups are raised from in-app notifications: off without in-app.
  const body = Object.fromEntries(types.value.map((k) => [k, { ...model[k]!, browser: model[k]!.in_app && model[k]!.browser }]))
  const me = await unwrap(api.PATCH('/me', { body: { notification_prefs: body } }))
  session.set({ ...(session.me ?? {}), ...(me as unknown as Me) } as Me)
  browser.setSoundEnabled(sound.value)
  emit('saved')
})

const forcedHint = computed(() => types.value.some((k) => EMAIL_FORCED.includes(k)))
const anyBrowser = computed(() => types.value.some((k) => model[k]!.browser))
const label = (k: string) => t(`notifications.types.${typeKey(k)}.label`)
</script>

<template>
  <v-form ref="formRef" data-testid="notification-prefs-form" @submit.prevent="onSave">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <div class="fd-matrix mb-2" role="table">
      <div class="fd-matrix-head d-none d-sm-grid" role="row">
        <div role="columnheader">{{ $t('notifications.prefs.type') }}</div>
        <div role="columnheader" class="text-center">{{ $t('notifications.prefs.inApp') }}</div>
        <div role="columnheader" class="text-center">{{ $t('notifications.prefs.email') }}</div>
        <div role="columnheader" class="text-center">{{ $t('notifications.prefs.browser') }}</div>
      </div>
      <div v-for="k in types" :key="k" class="fd-prefs-row" role="row" :data-testid="`prefs-row-${k}`">
        <div class="fd-prefs-label font-weight-regular" role="rowheader">{{ label(k) }}</div>
        <div class="fd-prefs-channel-group" role="presentation">
          <div class="fd-prefs-channel" role="cell">
            <span class="fd-prefs-channel-label d-sm-none">{{ $t('notifications.prefs.inAppShort') }}</span>
            <v-checkbox
              v-model="model[k]!.in_app"
              :aria-label="`${label(k)}: ${$t('notifications.prefs.inApp')}`"
              density="compact"
              hide-details
              class="fd-cell"
              :data-testid="`prefs-${k}-in_app`"
            />
          </div>
          <div class="fd-prefs-channel" role="cell">
            <span class="fd-prefs-channel-label d-sm-none">{{ $t('notifications.prefs.emailShort') }}</span>
            <v-checkbox
              v-model="model[k]!.email"
              :aria-label="`${label(k)}: ${$t('notifications.prefs.email')}`"
              :disabled="EMAIL_FORCED.includes(k)"
              :error-messages="fieldErrors[`notification_prefs.${k}.email`]"
              density="compact"
              hide-details="auto"
              class="fd-cell"
              :data-testid="`prefs-${k}-email`"
            />
          </div>
          <div class="fd-prefs-channel" role="cell">
            <span class="fd-prefs-channel-label d-sm-none">{{ $t('notifications.prefs.browserShort') }}</span>
            <v-checkbox
              v-model="model[k]!.browser"
              :aria-label="`${label(k)}: ${$t('notifications.prefs.browser')}`"
              :disabled="!model[k]!.in_app"
              :rules="browserRules"
              density="compact"
              hide-details="auto"
              class="fd-cell"
              :data-testid="`prefs-${k}-browser`"
            />
          </div>
        </div>
      </div>
    </div>
    <p v-if="forcedHint" class="text-caption text-medium-emphasis mb-3">{{ $t('notifications.prefs.emailForced') }}</p>

    <div class="text-subtitle-2 mb-1">{{ $t('notifications.prefs.browserTitle') }}</div>
    <p class="text-body-2 text-medium-emphasis mb-2">{{ $t('notifications.prefs.browserHint') }}</p>
    <v-alert v-if="permission === 'unsupported'" type="info" variant="tonal" density="compact" class="mb-2" data-testid="prefs-permission-unsupported">
      {{ $t('notifications.prefs.unsupported') }}
    </v-alert>
    <v-alert v-else-if="permission === 'denied'" type="warning" variant="tonal" density="compact" class="mb-2" data-testid="prefs-permission-denied">
      {{ $t('notifications.prefs.denied') }}
    </v-alert>
    <v-alert v-else-if="permission === 'granted'" type="success" variant="tonal" density="compact" class="mb-2" data-testid="prefs-permission-granted">
      {{ $t('notifications.prefs.granted') }}
    </v-alert>
    <v-btn v-else variant="tonal" class="mb-2" :color="anyBrowser ? 'primary' : undefined" data-testid="prefs-enable-browser" @click="enable">
      {{ $t('notifications.prefs.enable') }}
    </v-btn>
    <v-switch
      v-model="sound"
      :label="$t('notifications.prefs.sound')"
      :disabled="permission === 'unsupported'"
      color="primary"
      density="compact"
      hide-details
      data-testid="prefs-sound"
    />
    <div class="d-flex justify-end mt-2">
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="prefs-save">{{ $t('notifications.prefs.save') }}</v-btn>
    </div>
  </v-form>
</template>

<style scoped>
.fd-matrix-head,
.fd-prefs-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) repeat(3, 4.5rem);
  column-gap: 8px;
  align-items: center;
}
.fd-matrix-head {
  font-size: 0.75rem;
  font-weight: 600;
  color: rgba(var(--v-theme-on-surface), var(--v-medium-emphasis-opacity));
  padding-bottom: 4px;
  border-bottom: thin solid rgba(var(--v-border-color), var(--v-border-opacity));
}
.fd-prefs-row {
  padding: 6px 0;
  border-bottom: thin solid rgba(var(--v-border-color), var(--v-border-opacity));
}
.fd-prefs-label { white-space: normal; overflow-wrap: anywhere; line-height: 1.3; }
.fd-prefs-channel-group { display: contents; }
.fd-prefs-channel { display: flex; justify-content: center; }
.fd-prefs-channel-label { font-size: 0.6875rem; line-height: 1.2; text-align: center; margin-bottom: 2px; color: rgba(var(--v-theme-on-surface), var(--v-medium-emphasis-opacity)); }
.fd-cell { display: flex; justify-content: center; }
@media (max-width: 599.98px) {
  .fd-prefs-row { grid-template-columns: 1fr; row-gap: 6px; }
  .fd-prefs-channel-group {
    display: flex;
    justify-content: space-between;
    gap: 4px;
  }
  .fd-prefs-channel { flex: 1 1 0; min-width: 0; flex-direction: column; align-items: center; }
}
</style>
