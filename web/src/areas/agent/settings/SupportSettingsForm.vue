<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import AutocompleteSearch from '@/components/AutocompleteSearch.vue'
/**
 * Support settings form (FE-029): tier labels, auto-close and reopen windows,
 * registration and priority policies. Data in through props; nothing loads on mount.
 */
import { computed, reactive } from 'vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import { useConfigStore } from '@/stores/config'
import { SUPPORTED_LOCALES } from '@/i18n'
import type { components } from '@/api/schema'

type S = components['schemas']
const props = defineProps<{ settings?: S['SupportSettings'] | null }>()
const emit = defineEmits<{ saved: [S['SupportSettings']] }>()
const config = useConfigStore()

const s = props.settings ?? {}
const model = reactive({
  tier1: s.tier_labels?.['1'] ?? '',
  tier2: s.tier_labels?.['2'] ?? '',
  tier3: s.tier_labels?.['3'] ?? '',
  auto_close_days: s.auto_close_days === undefined ? '' : String(s.auto_close_days),
  reopen_window_days: s.reopen_window_days === undefined ? '' : String(s.reopen_window_days),
  self_registration: s.self_registration ?? false,
  customer_urgent_allowed: s.customer_urgent_allowed ?? false,
  urgent_requires_supervisor: s.urgent_requires_supervisor ?? false,
  triage_visible_to_agents: s.triage_visible_to_agents ?? true,
  portal_enabled: s.portal_enabled ?? true,
  kb_enabled: s.kb_enabled ?? true,
  kb_sitemap_enabled: s.kb_sitemap_enabled ?? true,
  default_locale: (s.default_locale ?? 'en') as S['Locale'],
})
const emailOff = computed(() => !config.config.email_enabled)
const labelRules = [validations.required.any, validations.maxLength(30)]
const dayRules = [validations.required.any, validations.integer, validations.range(1, 365)]
const LOCALE_NAMES: Record<string, string> = { en: 'English', it: 'Italiano', de: 'Deutsch', fr: 'Français', es: 'Español' }
const locales = SUPPORTED_LOCALES.map((l) => ({ value: l, title: LOCALE_NAMES[l] ?? l }))

const { formRef, submit, fieldErrors, formError, submitting } = useForm()
const onSave = submit(async () => {
  const body: S['SupportSettings'] = {
    tier_labels: { 1: model.tier1.trim(), 2: model.tier2.trim(), 3: model.tier3.trim() },
    auto_close_days: Number(model.auto_close_days),
    reopen_window_days: Number(model.reopen_window_days),
    customer_urgent_allowed: model.customer_urgent_allowed,
    urgent_requires_supervisor: model.urgent_requires_supervisor,
    triage_visible_to_agents: model.triage_visible_to_agents,
    portal_enabled: model.portal_enabled,
    kb_enabled: model.kb_enabled,
    kb_sitemap_enabled: model.kb_sitemap_enabled,
    default_locale: model.default_locale,
  }
  // While email is off, registration cannot be switched on (AUTH-016); leave it untouched.
  if (!emailOff.value) body.self_registration = model.self_registration
  const saved = await unwrap(api.PATCH('/admin/settings', { body }))
  emit('saved', saved)
})
</script>

<template>
  <v-form ref="formRef" data-testid="support-settings-form" @submit.prevent="onSave">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <h2 class="text-subtitle-1 mb-2">{{ $t('agent.settings.tiers') }}</h2>
    <p class="text-body-2 text-medium-emphasis mb-3">{{ $t('agent.settings.tiersHint') }}</p>
    <v-row density="compact">
      <v-col cols="12" sm="4">
        <v-text-field v-model="model.tier1" :label="$t('agent.settings.tierLabel', { n: 1 })" :rules="labelRules" :error-messages="fieldErrors['tier_labels.1']" data-testid="tier-1" />
      </v-col>
      <v-col cols="12" sm="4">
        <v-text-field v-model="model.tier2" :label="$t('agent.settings.tierLabel', { n: 2 })" :rules="labelRules" :error-messages="fieldErrors['tier_labels.2']" data-testid="tier-2" />
      </v-col>
      <v-col cols="12" sm="4">
        <v-text-field v-model="model.tier3" :label="$t('agent.settings.tierLabel', { n: 3 })" :rules="labelRules" :error-messages="fieldErrors['tier_labels.3']" data-testid="tier-3" />
      </v-col>
    </v-row>

    <h2 class="text-subtitle-1 mb-2 mt-2">{{ $t('agent.settings.lifecycle') }}</h2>
    <v-row density="compact">
      <v-col cols="12" sm="6">
        <v-text-field
          v-model="model.auto_close_days"
          :label="$t('agent.settings.autoCloseDays')"
          :hint="$t('agent.settings.autoCloseHint')"
          persistent-hint
          inputmode="numeric"
          :rules="dayRules"
          :error-messages="fieldErrors.auto_close_days"
          data-testid="auto-close-days"
        />
      </v-col>
      <v-col cols="12" sm="6">
        <v-text-field
          v-model="model.reopen_window_days"
          :label="$t('agent.settings.reopenDays')"
          :hint="$t('agent.settings.reopenHint')"
          persistent-hint
          inputmode="numeric"
          :rules="dayRules"
          :error-messages="fieldErrors.reopen_window_days"
          data-testid="reopen-days"
        />
      </v-col>
    </v-row>

    <h2 class="text-subtitle-1 mb-1 mt-4">{{ $t('agent.settings.policies') }}</h2>
    <v-switch
      v-model="model.portal_enabled"
      :label="$t('agent.settings.portalEnabled')"
      :hint="$t('agent.settings.portalHint')"
      persistent-hint
      color="primary"
      inset
      class="mb-2"
      data-testid="portal-enabled"
    />
    <v-alert v-if="!model.portal_enabled" type="warning" variant="tonal" density="compact" class="mb-3" data-testid="portal-off-warning">
      {{ $t('agent.settings.portalOffWarning') }}
    </v-alert>
    <v-switch
      v-model="model.self_registration"
      :label="$t('agent.settings.selfRegistration')"
      :disabled="emailOff"
      :hint="emailOff ? $t('agent.settings.selfRegistrationEmailOff') : ''"
      :persistent-hint="emailOff"
      color="primary"
      inset
      data-testid="self-registration"
    />
    <v-switch v-model="model.customer_urgent_allowed" :label="$t('agent.settings.customerUrgent')" color="primary" inset hide-details />
    <v-switch v-model="model.urgent_requires_supervisor" :label="$t('agent.settings.urgentSupervisor')" color="primary" inset hide-details />
    <v-switch v-model="model.triage_visible_to_agents" :label="$t('agent.settings.triageVisible')" color="primary" inset hide-details />
    <v-switch v-model="model.kb_enabled" :label="$t('agent.settings.kbEnabled')" color="primary" inset hide-details />
    <v-switch v-model="model.kb_sitemap_enabled" :label="$t('agent.settings.kbSitemap')" :disabled="!model.kb_enabled" color="primary" inset hide-details />
    <AutocompleteSearch v-model="model.default_locale" :items="locales" :label="$t('agent.settings.defaultLocale')" class="mt-4" style="max-width: 320px" />

    <div class="d-flex justify-end mt-2">
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="settings-save">{{ $t('agent.common.saveChanges') }}</v-btn>
    </div>
  </v-form>
</template>
