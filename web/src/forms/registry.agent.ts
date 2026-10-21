// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Agent-panel forms (FE-005), one list per area group so the groups can grow
 * independently. Spread into `formRegistry` in ./registry.ts.
 */
import type { FormEntry } from './registry'
import { forms as tickets } from '@/areas/agent/tickets/forms.registry'
import { forms as ticketList } from '@/areas/agent/tickets/list/forms.registry'
import { forms as directory } from '@/areas/agent/directory/forms.registry'
import { forms as settings } from '@/areas/agent/settings/forms.registry'
import { forms as config } from '@/areas/agent/config/forms.registry'
import { forms as content } from '@/areas/agent/content/forms.registry'
import { forms as automation } from '@/areas/agent/automation/forms.registry'
import { forms as privacy } from '@/areas/agent/privacy/forms.registry'

export const agentFormRegistry: FormEntry[] = [...tickets, ...ticketList, ...directory, ...settings, ...config, ...content, ...automation, ...privacy]
