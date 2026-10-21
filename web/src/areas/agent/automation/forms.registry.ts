// SPDX-License-Identifier: AGPL-3.0-only

import type { FormEntry } from '@/forms/registry'

/** Forms of the agent panel's `automation` group (D-19 A, FE-063/FE-064; aggregated by src/forms/registry.agent.ts). */
export const forms: FormEntry[] = [
  {
    file: 'areas/agent/automation/RuleForm.vue',
    load: () => import('./RuleForm.vue'),
    props: { teams: [], categories: [], staff: [], canned: [], customFields: [], organizations: [] },
    required: ['automation.rules.name', 'automation.rules.event', 'automation.rules.action'],
  },
  {
    file: 'areas/agent/automation/DryRunForm.vue',
    load: () => import('./DryRunForm.vue'),
    props: { rule: null },
    required: ['automation.dryRun.ticket'],
  },
  {
    file: 'areas/agent/automation/MacroForm.vue',
    load: () => import('./MacroForm.vue'),
    props: { staff: [], tags: [] },
    required: ['automation.macros.name'],
  },
]
