// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Every component that renders a <v-form> is registered here (FE-005).
 * `forms.spec.ts` fails if a .vue file contains <v-form> but is missing from
 * this list, then mounts each entry, submits it empty and asserts that no
 * request is sent — proving `validate()` runs before every submit.
 */
import type { Component } from 'vue'
import { agentFormRegistry } from './registry.agent'
import { portalFormRegistry } from './registry.portal'
import { forms as notificationForms } from '@/notifications/forms.registry'

export interface FormEntry {
  /** Path relative to src/, e.g. 'forms/LocalLoginForm.vue' */
  file: string
  load: () => Promise<{ default: Component }>
  props?: Record<string, unknown>
  /** Labels of required fields (documentation for reviewers and the test report). */
  required: string[]
  /**
   * Text expected after an empty submit. Defaults to the `required` message;
   * forms whose only rules are format rules (e.g. a signature length cap) set
   * `false` and are still checked for "no request sent".
   */
  emptyMessage?: string | false
}

export const formRegistry: FormEntry[] = [
  {
    file: 'forms/LocalLoginForm.vue',
    load: () => import('./LocalLoginForm.vue'),
    props: { audience: 'customer' },
    required: ['auth.email', 'auth.password'],
  },
  // MFA (D-19 §B, FE-070…): shared by both areas' login and profile pages.
  {
    file: 'components/mfa/MfaCodeForm.vue',
    load: () => import('@/components/mfa/MfaCodeForm.vue'),
    props: { kind: 'totp', action: () => Promise.resolve() },
    required: ['mfa.code'],
  },
  {
    file: 'components/mfa/MfaReauthForm.vue',
    load: () => import('@/components/mfa/MfaReauthForm.vue'),
    required: ['auth.password'],
  },
  ...agentFormRegistry,
  ...portalFormRegistry,
  ...notificationForms,
]
