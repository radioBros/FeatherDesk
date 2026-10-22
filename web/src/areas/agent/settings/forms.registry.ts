// SPDX-License-Identifier: AGPL-3.0-only

import type { FormEntry } from '@/forms/registry'

/** Forms of the agent panel's `settings` group (FE-005; aggregated by src/forms/registry.agent.ts). */
export const forms: FormEntry[] = [
  {
    file: 'areas/agent/settings/SupportSettingsForm.vue',
    load: () => import('./SupportSettingsForm.vue'),
    props: { settings: null },
    required: ['agent.settings.tierLabel', 'agent.settings.autoCloseDays', 'agent.settings.reopenDays'],
  },
  {
    file: 'areas/agent/settings/AuditFilterForm.vue',
    load: () => import('./AuditFilterForm.vue'),
    props: {},
    required: [],
    // Filters are all optional; an invalid date still blocks the request.
    emptyMessage: false,
  },
  {
    file: 'areas/agent/settings/BrandingForm.vue',
    load: () => import('./BrandingForm.vue'),
    props: { settings: null },
    required: ['agent.branding.productName', 'agent.branding.primary'],
  },
  {
    file: 'areas/agent/settings/EmailSettingsForm.vue',
    load: () => import('./EmailSettingsForm.vue'),
    props: { settings: null },
    required: ['agent.email.host', 'agent.email.port', 'agent.email.security', 'agent.email.fromName', 'agent.email.fromAddress'],
  },
  {
    file: 'areas/agent/settings/EmailTestForm.vue',
    load: () => import('./EmailTestForm.vue'),
    props: { config: null },
    required: ['agent.email.testRecipient'],
  },
  {
    file: 'areas/agent/settings/SecurityPolicyForm.vue',
    load: () => import('./SecurityPolicyForm.vue'),
    props: { settings: null },
    required: ['mfa.policy.label'],
    emptyMessage: 'Choose an option',
  },
]
