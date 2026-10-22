// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Portal, KB and setup-wizard forms (FE-005 registry, merged into ./registry).
 * Every form here is prop-driven and fetches nothing on mount, so an empty
 * submit must send no request at all.
 */
import type { FormEntry } from './registry'

const p = (file: string, load: FormEntry['load'], required: string[], props?: Record<string, unknown>): FormEntry => ({
  file,
  load,
  required,
  props,
})

export const portalFormRegistry: FormEntry[] = [
  p('areas/portal/forms/RegisterForm.vue', () => import('@/areas/portal/forms/RegisterForm.vue'), ['portal.account.name', 'auth.email', 'auth.password', 'portal.account.confirmPassword']),
  p('areas/portal/forms/ForgotPasswordForm.vue', () => import('@/areas/portal/forms/ForgotPasswordForm.vue'), ['auth.email']),
  p('areas/portal/forms/ResetPasswordForm.vue', () => import('@/areas/portal/forms/ResetPasswordForm.vue'), ['portal.account.newPassword', 'portal.account.confirmPassword'], { token: 'reset-token' }),
  p('areas/portal/forms/MagicLinkForm.vue', () => import('@/areas/portal/forms/MagicLinkForm.vue'), ['auth.email']),
  p('areas/portal/forms/InviteForm.vue', () => import('@/areas/portal/forms/InviteForm.vue'), ['portal.account.name', 'auth.password', 'portal.account.confirmPassword'], { token: 'invite-token' }),
  p('areas/portal/forms/NewTicketForm.vue', () => import('@/areas/portal/forms/NewTicketForm.vue'), ['portal.tickets.subject', 'portal.newTicket.description']),
  p('areas/portal/forms/ReplyForm.vue', () => import('@/areas/portal/forms/ReplyForm.vue'), ['portal.detail.yourReply'], { ticketId: 't1' }),
  p('areas/portal/forms/CsatForm.vue', () => import('@/areas/portal/forms/CsatForm.vue'), ['portal.csat.question'], { ticketId: 't1' }),
  p('areas/portal/forms/ProfileForm.vue', () => import('@/areas/portal/forms/ProfileForm.vue'), ['portal.account.name']),
  p('areas/portal/forms/PasswordForm.vue', () => import('@/areas/portal/forms/PasswordForm.vue'), ['portal.profile.currentPassword', 'portal.account.newPassword']),
  p('areas/portal/forms/TokenForm.vue', () => import('@/areas/portal/forms/TokenForm.vue'), ['portal.tokens.name', 'portal.tokens.scopes']),
  p('areas/portal/forms/OrgInviteForm.vue', () => import('@/areas/portal/forms/OrgInviteForm.vue'), ['portal.account.name', 'auth.email']),
  p('components/privacy/ErasureRequestForm.vue', () => import('@/components/privacy/ErasureRequestForm.vue'), ['privacy.my.confirm']),
  p('areas/setup/forms/SetupTokenForm.vue', () => import('@/areas/setup/forms/SetupTokenForm.vue'), ['setup.token.label']),
  p('areas/setup/forms/SetupAdminForm.vue', () => import('@/areas/setup/forms/SetupAdminForm.vue'), ['setup.admin.name', 'auth.email', 'auth.password']),
  p('areas/setup/forms/SetupBrandingForm.vue', () => import('@/areas/setup/forms/SetupBrandingForm.vue'), ['setup.branding.productName'], { initial: { product_name: '', primary_color: '' } }),
  p('areas/setup/forms/SetupSmtpForm.vue', () => import('@/areas/setup/forms/SetupSmtpForm.vue'), ['setup.smtp.host', 'setup.smtp.port', 'setup.smtp.fromAddress']),
]
