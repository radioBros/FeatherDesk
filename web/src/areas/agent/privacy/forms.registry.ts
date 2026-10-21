// SPDX-License-Identifier: AGPL-3.0-only

import type { FormEntry } from '@/forms/registry'

/** Forms of the agent panel's `privacy` group (FE-084…FE-086, D-19 §C; aggregated by src/forms/registry.agent.ts). */
export const forms: FormEntry[] = [
  {
    file: 'areas/agent/privacy/PrivacySettingsForm.vue',
    load: () => import('./PrivacySettingsForm.vue'),
    props: { settings: null },
    required: [
      'privacy.settings.closedTicketsMonths',
      'privacy.settings.closedTicketsAction',
      'privacy.settings.inboundEmailDays',
      'privacy.settings.auditLogMonths',
      'privacy.settings.exportDays',
      'privacy.settings.autoApproveDays',
      'privacy.settings.autoApproveMode',
    ],
  },
  {
    file: 'areas/agent/privacy/EraseForm.vue',
    load: () => import('./EraseForm.vue'),
    props: { userId: 'u-1' },
    required: ['privacy.mode.label', 'privacy.erase.confirm'],
  },
  {
    file: 'areas/agent/privacy/RejectForm.vue',
    load: () => import('./RejectForm.vue'),
    props: { requestId: 'r-1' },
    required: ['privacy.reject.reason'],
  },
]
