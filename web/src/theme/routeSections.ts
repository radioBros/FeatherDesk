// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Which section (hue) and icon a page belongs to: the admin nav entries and the
 * page-header badge (`components/PageHeader.vue`) share one table. Loaded only
 * by lazy chunks (the admin nav items and the pages), never by an initial one.
 */
import {
  mdiAccountGroupOutline,
  mdiBadgeAccountOutline,
  mdiBookOpenPageVariantOutline,
  mdiBriefcaseClockOutline,
  mdiCalendarClockOutline,
  mdiCogOutline,
  mdiDatabaseOutline,
  mdiEmailOutline,
  mdiEmailFastOutline,
  mdiFormTextboxPassword,
  mdiHistory,
  mdiKeyVariant,
  mdiLightningBolt,
  mdiMessageTextOutline,
  mdiPaletteOutline,
  mdiPlaylistCheck,
  mdiServerOutline,
  mdiShapeOutline,
  mdiShieldLockOutline,
  mdiTagOutline,
  mdiTimerOutline,
  mdiWebhook,
  mdiTextBoxMultipleOutline,
  mdiAccountCircleOutline,
  mdiAccountMultipleOutline,
  mdiBellOutline,
  mdiChartBar,
  mdiDomain,
  mdiHelpCircleOutline,
  mdiTicketOutline,
  mdiMonitorCellphone,
  mdiFolderMultipleOutline,
  mdiPuzzleOutline,
} from '@mdi/js'
import type { Section } from './palette'

export interface SectionMeta { icon: string; section: Section }

export const ADMIN_META: Record<string, SectionMeta> = {
  '/agent/admin/projects': { icon: mdiFolderMultipleOutline, section: 'projects' },
  '/agent/admin/teams': { icon: mdiAccountGroupOutline, section: 'teams' },
  '/agent/admin/staff': { icon: mdiBadgeAccountOutline, section: 'teams' },
  '/agent/admin/privacy': { icon: mdiShieldLockOutline, section: 'privacy' },
  '/agent/admin/categories': { icon: mdiShapeOutline, section: 'tickets' },
  '/agent/admin/sla': { icon: mdiTimerOutline, section: 'automation' },
  '/agent/admin/calendars': { icon: mdiCalendarClockOutline, section: 'settings' },
  '/agent/admin/canned': { icon: mdiMessageTextOutline, section: 'tickets' },
  '/agent/admin/tags': { icon: mdiTagOutline, section: 'branding' },
  '/agent/admin/automation': { icon: mdiLightningBolt, section: 'automation' },
  '/agent/admin/macros': { icon: mdiPlaylistCheck, section: 'automation' },
  '/agent/admin/custom-fields': { icon: mdiFormTextboxPassword, section: 'settings' },
  '/agent/admin/kb-categories': { icon: mdiBookOpenPageVariantOutline, section: 'kb' },
  '/agent/admin/email': { icon: mdiEmailOutline, section: 'email' },
  '/agent/admin/inbound': { icon: mdiEmailFastOutline, section: 'email' },
  '/agent/admin/integrations': { icon: mdiPuzzleOutline, section: 'email' },
  '/agent/admin/webhooks': { icon: mdiWebhook, section: 'email' },
  '/agent/admin/branding': { icon: mdiPaletteOutline, section: 'branding' },
  '/agent/admin/settings': { icon: mdiCogOutline, section: 'settings' },
  '/agent/admin/system': { icon: mdiServerOutline, section: 'system' },
  '/agent/admin/queue': { icon: mdiTextBoxMultipleOutline, section: 'system' },
  '/agent/admin/jobs': { icon: mdiBriefcaseClockOutline, section: 'system' },
  '/agent/admin/storage': { icon: mdiDatabaseOutline, section: 'system' },
  '/agent/admin/audit': { icon: mdiHistory, section: 'system' },
  '/agent/admin/tokens': { icon: mdiKeyVariant, section: 'system' },
}

const PREFIXES: [string, SectionMeta][] = [
  ['/agent/profile/tokens', { icon: mdiKeyVariant, section: 'system' }],
  ['/agent/profile/data', { icon: mdiShieldLockOutline, section: 'privacy' }],
  ['/agent/profile', { icon: mdiAccountCircleOutline, section: 'customers' }],
  ['/agent/tickets', { icon: mdiTicketOutline, section: 'tickets' }],
  ['/agent/customers', { icon: mdiAccountMultipleOutline, section: 'customers' }],
  ['/agent/organizations', { icon: mdiDomain, section: 'customers' }],
  ['/agent/kb', { icon: mdiBookOpenPageVariantOutline, section: 'kb' }],
  ['/agent/reports', { icon: mdiChartBar, section: 'reports' }],
  ['/agent/macros', { icon: mdiPlaylistCheck, section: 'automation' }],
  ['/agent/notifications', { icon: mdiBellOutline, section: 'notifications' }],
  ['/notifications', { icon: mdiBellOutline, section: 'notifications' }],
  ['/profile/tokens', { icon: mdiKeyVariant, section: 'system' }],
  ['/profile/sessions', { icon: mdiMonitorCellphone, section: 'system' }],
  ['/profile/security', { icon: mdiShieldLockOutline, section: 'privacy' }],
  ['/profile/data', { icon: mdiShieldLockOutline, section: 'privacy' }],
  ['/profile', { icon: mdiAccountCircleOutline, section: 'customers' }],
  ['/organization', { icon: mdiDomain, section: 'customers' }],
  ['/tickets', { icon: mdiTicketOutline, section: 'tickets' }],
  ['/kb', { icon: mdiHelpCircleOutline, section: 'kb' }],
]

/** The section of a route path, or undefined (no badge). */
export function sectionForPath(path: string): SectionMeta | undefined {
  const admin = Object.keys(ADMIN_META).find((k) => path === k || path.startsWith(k + '/'))
  if (admin) return ADMIN_META[admin]
  return PREFIXES.find(([p]) => path === p || path.startsWith(p + '/'))?.[1]
}
