<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import AutocompleteSearch from '@/components/AutocompleteSearch.vue'
/**
 * Privacy settings (FE-086, D-19 §C): the retention policy, automatic approval
 * of deletion requests and the policy version recorded with consents. "Preview"
 * runs the retention dry run with the form's current (unsaved) values; both
 * actions validate first. Data in through props; nothing loads on mount.
 */
import { computed, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { mdiEyeOutline } from '@mdi/js'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import RelativeTime from '@/components/RelativeTime.vue'
import type { components } from '@/api/schema'

type S = components['schemas']
const props = defineProps<{ settings?: S['PrivacySettings'] | null }>()
const emit = defineEmits<{ saved: [S['PrivacySettings']] }>()
const { t } = useI18n()

const s = props.settings
const str = (n: number | undefined) => (n === undefined ? '' : String(n))
const model = reactive({
  closed_tickets_months: str(s?.retention.closed_tickets_months),
  closed_tickets_action: (s?.retention.closed_tickets_action ?? null) as S['RetentionTicketAction'] | null,
  inbound_email_days: str(s?.retention.inbound_email_days),
  audit_log_months: str(s?.retention.audit_log_months),
  export_days: str(s?.retention.export_days),
  erasure_auto_approve_days: str(s?.erasure_auto_approve_days),
  erasure_auto_approve_mode: (s?.erasure_auto_approve_mode ?? null) as S['ErasureMode'] | null,
  policy_version: s?.policy_version ?? '',
})

const int = (min: number, max: number) => [validations.required.any, validations.integer, validations.range(min, max)]
const closedRules = int(0, 240)
const inboundRules = int(1, 90)
const auditRules = int(0, 120)
const exportRules = int(1, 30)
const autoDaysRules = int(0, 365)
const selectRules = [validations.required.select]
const versionRules = [validations.maxLength(64)]
const actions = computed(() => (['anonymize', 'purge'] as const).map((v) => ({ value: v, title: t(`privacy.settings.${v}`) })))
const modes = computed(() => (['anonymize', 'anonymize_and_redact'] as const).map((v) => ({ value: v, title: t(`privacy.mode.${v}`) })))

function retention(): S['RetentionPolicy'] {
  return {
    closed_tickets_months: Number(model.closed_tickets_months),
    closed_tickets_action: model.closed_tickets_action as S['RetentionTicketAction'],
    inbound_email_days: Number(model.inbound_email_days),
    audit_log_months: Number(model.audit_log_months),
    export_days: Number(model.export_days),
  }
}

const preview = ref<S['RetentionPreview'] | null>(null)
const { formRef, submit, fieldErrors, formError, submitting } = useForm()
const onSave = submit(async () => {
  const saved = await unwrap(
    api.PUT('/admin/privacy/settings', {
      body: {
        retention: retention(),
        erasure_auto_approve_days: Number(model.erasure_auto_approve_days),
        erasure_auto_approve_mode: model.erasure_auto_approve_mode as S['ErasureMode'],
        policy_version: model.policy_version.trim(),
      },
    }),
  )
  emit('saved', saved)
})
const onPreview = submit(async () => {
  preview.value = await unwrap(api.GET('/admin/privacy/retention/preview', { params: { query: retention() } }))
})
const counts = computed(() =>
  preview.value
    ? [
        { key: 'closedTickets', n: preview.value.closed_tickets, extra: t(`privacy.settings.${preview.value.closed_tickets_action}`) },
        { key: 'inboundEmails', n: preview.value.inbound_emails, extra: '' },
        { key: 'auditEntries', n: preview.value.audit_entries, extra: '' },
        { key: 'expiredExports', n: preview.value.expired_exports, extra: '' },
        { key: 'autoApprovals', n: preview.value.erasure_auto_approvals, extra: '' },
      ]
    : [],
)
</script>

<template>
  <v-form ref="formRef" data-testid="privacy-settings-form" @submit.prevent="onSave">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <h2 class="text-subtitle-1 mb-1">{{ $t('privacy.settings.retention') }}</h2>
    <p class="text-body-2 text-medium-emphasis mb-3">{{ $t('privacy.settings.retentionHint') }}</p>
    <v-row density="compact">
      <v-col cols="12" sm="6">
        <v-text-field
          v-model="model.closed_tickets_months"
          :label="$t('privacy.settings.closedTicketsMonths')"
          :hint="$t('privacy.settings.closedTicketsMonthsHint')"
          persistent-hint
          inputmode="numeric"
          :rules="closedRules"
          :error-messages="fieldErrors['retention.closed_tickets_months']"
          data-testid="closed-tickets-months"
        />
      </v-col>
      <v-col cols="12" sm="6">
        <AutocompleteSearch
          v-model="model.closed_tickets_action"
          :items="actions"
          :label="$t('privacy.settings.closedTicketsAction')"
          :rules="selectRules"
          :error-messages="fieldErrors['retention.closed_tickets_action']"
          data-testid="closed-tickets-action"
        />
      </v-col>
      <v-col cols="12" sm="4">
        <v-text-field
          v-model="model.inbound_email_days"
          :label="$t('privacy.settings.inboundEmailDays')"
          inputmode="numeric"
          :rules="inboundRules"
          :error-messages="fieldErrors['retention.inbound_email_days']"
          data-testid="inbound-email-days"
        />
      </v-col>
      <v-col cols="12" sm="4">
        <v-text-field
          v-model="model.audit_log_months"
          :label="$t('privacy.settings.auditLogMonths')"
          :hint="$t('privacy.settings.auditLogMonthsHint')"
          persistent-hint
          inputmode="numeric"
          :rules="auditRules"
          :error-messages="fieldErrors['retention.audit_log_months']"
          data-testid="audit-log-months"
        />
      </v-col>
      <v-col cols="12" sm="4">
        <v-text-field
          v-model="model.export_days"
          :label="$t('privacy.settings.exportDays')"
          inputmode="numeric"
          :rules="exportRules"
          :error-messages="fieldErrors['retention.export_days']"
          data-testid="export-days"
        />
      </v-col>
    </v-row>

    <h2 class="text-subtitle-1 mb-2 mt-2">{{ $t('privacy.settings.erasure') }}</h2>
    <v-row density="compact">
      <v-col cols="12" sm="6">
        <v-text-field
          v-model="model.erasure_auto_approve_days"
          :label="$t('privacy.settings.autoApproveDays')"
          :hint="$t('privacy.settings.autoApproveDaysHint')"
          persistent-hint
          inputmode="numeric"
          :rules="autoDaysRules"
          :error-messages="fieldErrors.erasure_auto_approve_days"
          data-testid="auto-approve-days"
        />
      </v-col>
      <v-col cols="12" sm="6">
        <AutocompleteSearch
          v-model="model.erasure_auto_approve_mode"
          :items="modes"
          :label="$t('privacy.settings.autoApproveMode')"
          :rules="selectRules"
          :error-messages="fieldErrors.erasure_auto_approve_mode"
          data-testid="auto-approve-mode"
        />
      </v-col>
      <v-col cols="12" sm="6">
        <v-text-field
          v-model="model.policy_version"
          :label="$t('privacy.settings.policyVersion')"
          :hint="$t('privacy.settings.policyVersionHint')"
          persistent-hint
          :rules="versionRules"
          :error-messages="fieldErrors.policy_version"
          data-testid="policy-version"
        />
      </v-col>
    </v-row>

    <v-card v-if="preview" border flat class="pa-3 mt-4" data-testid="retention-preview">
      <div class="text-subtitle-2">{{ $t('privacy.preview.title') }} · <RelativeTime :value="preview.as_of" /></div>
      <p class="text-body-2 text-medium-emphasis mb-2">{{ $t('privacy.preview.intro') }}</p>
      <v-list density="compact">
        <v-list-item v-for="c in counts" :key="c.key" :title="$t(`privacy.preview.${c.key}`)" :subtitle="c.extra" :data-testid="`preview-${c.key}`">
          <template #append><strong>{{ c.n }}</strong></template>
        </v-list-item>
      </v-list>
    </v-card>

    <div class="d-flex justify-end ga-2 mt-4">
      <v-btn variant="tonal" :prepend-icon="mdiEyeOutline" :loading="submitting" data-testid="retention-preview-btn" @click="onPreview">{{ $t('privacy.action.preview') }}</v-btn>
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="privacy-settings-save">{{ $t('privacy.action.save') }}</v-btn>
    </div>
  </v-form>
</template>
