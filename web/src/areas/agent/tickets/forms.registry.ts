// SPDX-License-Identifier: AGPL-3.0-only

import type { FormEntry } from '@/forms/registry'

const ticket = { id: 't-1', number: 1042, tier: 1, team_id: null, allowed_actions: ['reply', 'note', 'set_status'] }

/** Forms of the agent ticket detail and new-ticket views (FE-005; aggregated by src/forms/registry.agent.ts). */
export const forms: FormEntry[] = [
  { file: 'areas/agent/tickets/detail/ComposerForm.vue', load: () => import('./detail/ComposerForm.vue'), props: { ticket }, required: ['agent.ticket.composer.replyLabel'] },
  { file: 'areas/agent/tickets/detail/EscalateForm.vue', load: () => import('./detail/EscalateForm.vue'), props: { ticket, direction: 'escalate', teams: [] }, required: ['agent.ticket.escalate.reason'] },
  { file: 'areas/agent/tickets/detail/LinkForm.vue', load: () => import('./detail/LinkForm.vue'), props: { ticketId: 't-1' }, required: ['agent.ticket.links.target', 'agent.ticket.links.typeLabel'] },
  { file: 'areas/agent/tickets/detail/MergeForm.vue', load: () => import('./detail/MergeForm.vue'), props: { ticket }, required: ['agent.ticket.merge.target'] },
  { file: 'areas/agent/tickets/detail/SplitForm.vue', load: () => import('./detail/SplitForm.vue'), props: { ticketId: 't-1', selected: [] }, required: ['agent.ticket.split.subject', 'selected messages'] },
  { file: 'areas/agent/tickets/detail/EditNoteForm.vue', load: () => import('./detail/EditNoteForm.vue'), props: { ticketId: 't-1', message: { id: 'm-1', body_md: '' } }, required: ['body'] },
  {
    file: 'areas/agent/tickets/detail/CustomFieldsForm.vue',
    load: () => import('./detail/CustomFieldsForm.vue'),
    props: {
      editable: true,
      values: {},
      save: () => fetch('/api/v1/should-not-be-called'),
      defs: [{ id: 'cf-1', key: 'order_id', type: 'text', label: { en: 'Order id' }, required: true, active: true, sort: 1, customer_visible: false, customer_editable: false }],
    },
    required: ['custom field (required: true)'],
  },
  { file: 'areas/agent/tickets/detail/TicketCreateForm.vue', load: () => import('./detail/TicketCreateForm.vue'), props: {}, required: ['agent.newTicket.requester', 'agent.newTicket.subject', 'agent.newTicket.body'] },
]
