// SPDX-License-Identifier: AGPL-3.0-only

import type { FormEntry } from '@/forms/registry'

/** Forms of the agent panel's `content` group (FE-005; aggregated by src/forms/registry.agent.ts). */
export const forms: FormEntry[] = [
  {
    file: 'areas/agent/content/KbArticleForm.vue',
    load: () => import('./KbArticleForm.vue'),
    props: { categories: [], locales: ['en', 'it'] },
    required: ['agent.common.category', 'agent.kb.visibility', 'agent.kb.titleCol'],
  },
  {
    file: 'areas/agent/content/KbCategoryForm.vue',
    load: () => import('./KbCategoryForm.vue'),
    props: { categories: [] },
    required: ['agent.kbCategories.title', 'agent.kb.slug', 'agent.kb.visibility'],
  },
  {
    file: 'areas/agent/content/InboundMailboxForm.vue',
    load: () => import('./InboundMailboxForm.vue'),
    props: { teams: [] },
    required: ['agent.common.name', 'agent.inbound.kindLabel', 'agent.inbound.address'],
  },
  {
    file: 'areas/agent/content/ReportsFilterForm.vue',
    load: () => import('./ReportsFilterForm.vue'),
    props: { teams: [] },
    required: ['agent.reports.from', 'agent.reports.to'],
  },
]
