// SPDX-License-Identifier: AGPL-3.0-only

import type { FormEntry } from '@/forms/registry'

/** Forms of the agent ticket list and layout (FE-005; aggregated by src/forms/registry.agent.ts). */
export const forms: FormEntry[] = [
  {
    file: 'areas/agent/tickets/list/SaveViewForm.vue',
    load: () => import('./SaveViewForm.vue'),
    props: { filter: { status: 'open' } },
    required: ['agent.views.name'],
  },
  {
    file: 'areas/agent/tickets/list/BulkActionForm.vue',
    load: () => import('./BulkActionForm.vue'),
    props: { tickets: [{ id: 't-1', number: 1 }], staff: [], teams: [] },
    required: ['agent.bulk.action', 'agent.bulk.value'],
    emptyMessage: 'Choose an option',
  },
]
