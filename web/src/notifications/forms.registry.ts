// SPDX-License-Identifier: AGPL-3.0-only

import type { FormEntry } from '@/forms/registry'

/** Forms of the notification centre (D-19 §D; aggregated by src/forms/registry.ts). */
export const forms: FormEntry[] = [
  {
    // Only checkboxes: submitted with a browser channel on but no browser
    // permission it is invalid, so the registry test checks nothing is sent.
    file: 'notifications/NotificationPrefsForm.vue',
    load: () => import('./NotificationPrefsForm.vue'),
    props: { area: 'agent', permission: 'default', prefs: { 'staff.assigned': { in_app: true, email: true, browser: true } } },
    required: [],
    emptyMessage: false,
  },
]
