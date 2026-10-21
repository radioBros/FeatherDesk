// SPDX-License-Identifier: AGPL-3.0-only

import type { FormEntry } from '@/forms/registry'

/** Forms of the agent panel's `directory` group (FE-005; aggregated by src/forms/registry.agent.ts). */
export const forms: FormEntry[] = [
  {
    file: 'areas/agent/directory/admin/CategoryForm.vue',
    load: () => import('./admin/CategoryForm.vue'),
    props: { teams: [] },
    required: ['agent.common.name'],
  },
  {
    file: 'areas/agent/directory/CustomerForm.vue',
    load: () => import('./CustomerForm.vue'),
    props: { organizations: [] },
    required: ['agent.common.name', 'agent.common.email'],
  },
  {
    file: 'areas/agent/directory/OrganizationForm.vue',
    load: () => import('./OrganizationForm.vue'),
    required: ['agent.common.name'],
  },
  {
    file: 'areas/agent/directory/admin/StaffForm.vue',
    load: () => import('./admin/StaffForm.vue'),
    props: { teams: [] },
    required: ['agent.common.name', 'agent.common.email'],
  },
  {
    file: 'areas/agent/directory/admin/TeamForm.vue',
    load: () => import('./admin/TeamForm.vue'),
    props: { calendars: [] },
    required: ['agent.common.name'],
  },
  {
    file: 'areas/agent/directory/TokenForm.vue',
    load: () => import('./TokenForm.vue'),
    required: ['agent.common.name', 'agent.tokens.scopes'],
  },
  {
    file: 'areas/agent/directory/ProfileForm.vue',
    load: () => import('./ProfileForm.vue'),
    required: ['agent.common.name', 'app.language'],
  },
  {
    file: 'areas/agent/directory/PasswordForm.vue',
    load: () => import('./PasswordForm.vue'),
    required: ['agent.profile.currentPassword', 'agent.profile.newPassword', 'agent.profile.confirmPassword'],
  },
  {
    // Only a length cap: submitted empty it is invalid only when over 2000
    // characters, so the registry test checks a too-long draft sends nothing.
    file: 'areas/agent/directory/SignatureForm.vue',
    load: () => import('./SignatureForm.vue'),
    props: { signature: 'x'.repeat(2001) },
    required: [],
    emptyMessage: 'Enter at most 2000 characters',
  },
]
