// SPDX-License-Identifier: AGPL-3.0-only

import type { FormEntry } from '@/forms/registry'

/** Forms of the agent panel's `config` group (FE-005; aggregated by src/forms/registry.agent.ts). */
export const forms: FormEntry[] = [
  {
    file: 'areas/agent/config/WebhookForm.vue',
    load: () => import('./WebhookForm.vue'),
    required: ['agent.webhooks.url', 'agent.webhooks.events'],
  },
  {
    file: 'areas/agent/config/SlaForm.vue',
    load: () => import('./SlaForm.vue'),
    props: { policies: [] },
    required: ['agent.sla.firstResponse', 'agent.sla.resolution'],
  },
  {
    file: 'areas/agent/config/CannedForm.vue',
    load: () => import('./CannedForm.vue'),
    props: { teams: [] },
    required: ['agent.canned.title', 'agent.canned.body'],
  },
  {
    file: 'areas/agent/config/CustomFieldForm.vue',
    load: () => import('./CustomFieldForm.vue'),
    props: { categories: [] },
    required: ['agent.customFields.key', 'agent.customFields.label', 'agent.customFields.type'],
  },
  {
    file: 'areas/agent/config/CalendarForm.vue',
    load: () => import('./CalendarForm.vue'),
    required: ['agent.common.name', 'agent.calendars.timezone'],
  },
]
