// SPDX-License-Identifier: AGPL-3.0-only

import type { FormEntry } from '@/forms/registry'

/** Forms of the Projects admin (D-20; aggregated by src/forms/registry.agent.ts). */
export const forms: FormEntry[] = [
  {
    file: 'areas/agent/projects/ProjectForm.vue',
    load: () => import('./ProjectForm.vue'),
    props: { teams: [], support: true },
    required: ['projects.admin.key', 'agent.common.name'],
  },
  {
    // One overridden priority with unusable minutes: submitted as is, it must not send anything.
    file: 'areas/agent/projects/ProjectSla.vue',
    load: () => import('./ProjectSla.vue'),
    props: {
      projectId: 'p-1',
      policies: [{ priority: 'normal', first_response_minutes: 0, resolution_minutes: 0, calendar_mode: '24x7', inherited: false }],
    },
    required: ['agent.sla.firstResponse', 'agent.sla.resolution'],
    emptyMessage: false,
  },
]
