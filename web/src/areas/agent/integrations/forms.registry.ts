// SPDX-License-Identifier: AGPL-3.0-only

import type { FormEntry } from '@/forms/registry'

/** Forms of the Integrations area (D-21; aggregated by src/forms/registry.agent.ts). */
export const forms: FormEntry[] = [
  {
    file: 'areas/agent/integrations/slack/SlackAppForm.vue',
    load: () => import('./slack/SlackAppForm.vue'),
    required: ['agent.integrations.slack.setup.clientId', 'agent.integrations.slack.setup.clientSecret'],
  },
  {
    file: 'areas/agent/integrations/slack/SlackRouteForm.vue',
    load: () => import('./slack/SlackRouteForm.vue'),
    props: { channels: [] },
    required: ['agent.integrations.slack.form.channel', 'agent.integrations.slack.form.events'],
  },
]
