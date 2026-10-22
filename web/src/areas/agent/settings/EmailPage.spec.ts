// SPDX-License-Identifier: AGPL-3.0-only

import EmailPage from './EmailPage.vue'
import EmailSettingsForm from './EmailSettingsForm.vue'
import EmailTestForm from './EmailTestForm.vue'
import { mountAgent, fx, json, byTestId, typeInto, click } from '../testing'
import type { components } from '@/api/schema'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const EMAIL: components['schemas']['EmailSettings'] = {
  smtp: { host: 'smtp.example.com', port: 587, security: 'starttls', auth: 'plain', username: 'mailer', password_set: true, timeout_seconds: 10 },
  senders: {
    customer: { from_name: 'Acme Support', from_address: 'support@example.com' },
    staff: { from_name: 'Acme Desk', from_address: 'desk@example.com' },
  },
  templates_enabled: { 'customer.reply': true, 'staff.sla': false },
  triage_mailbox: null,
  status: { enabled: true },
}

const input = (id: string) => byTestId(id)!.querySelector('input') as HTMLInputElement

async function mountPage(settings = EMAIL, api: Record<string, unknown> = {}) {
  return mountAgent(EmailPage, { me: fx.me('admin'), api: { 'GET /admin/email': settings, 'GET /auth/config': {}, ...api } as never })
}

describe('EmailPage (FE-048)', () => {
  it('never pre-fills the password and leaves it out of the PUT when untouched', async () => {
    const h = await mountPage(EMAIL, { 'PUT /admin/email': (_u: URL, init: { body?: any }) => json({ ...EMAIL, ...init.body }) })
    await h.flush()
    expect(input('smtp-password').value).toBe('')
    expect(byTestId('password-set')).toBeTruthy()
    await click(byTestId('email-save'))
    await h.flush()
    const body = h.calls('PUT /admin/email')[0]!.body as components['schemas']['EmailSettings']
    expect(body.smtp).not.toHaveProperty('password')
    expect(body.smtp).toMatchObject({ host: 'smtp.example.com', port: 587, security: 'starttls' })
    expect(body).not.toHaveProperty('status')
    expect(body.templates_enabled).toMatchObject({ 'customer.reply': true, 'staff.sla': false })
    expect(Object.keys(body.templates_enabled ?? {}).some((k) => k.startsWith('auth.'))).toBe(false)
  })

  it('sends a newly typed password', async () => {
    const h = await mountPage(EMAIL, { 'PUT /admin/email': json(EMAIL) })
    await h.flush()
    await typeInto(byTestId('smtp-password'), 'n3w-secret')
    await click(byTestId('email-save'))
    await h.flush()
    expect((h.calls('PUT /admin/email')[0]!.body as any).smtp.password).toBe('n3w-secret')
  })

  it('disables, badges and omits env-locked fields', async () => {
    const h = await mountPage({ ...EMAIL, locked_by_env: ['smtp.host', 'smtp.password'] }, { 'PUT /admin/email': json(EMAIL) })
    await h.flush()
    expect(input('smtp-host').disabled).toBe(true)
    expect(byTestId('smtp-host')!.textContent).toContain('Set by environment')
    expect(input('smtp-password').disabled).toBe(true)
    await click(byTestId('email-save'))
    await h.flush()
    const body = h.calls('PUT /admin/email')[0]!.body as any
    expect(body.smtp).not.toHaveProperty('host')
    expect(body.smtp).not.toHaveProperty('password')
    expect(body).not.toHaveProperty('locked_by_env')
  })

  it('rejects port 70000 client-side', async () => {
    const h = await mountPage(EMAIL, { 'PUT /admin/email': json(EMAIL) })
    await h.flush()
    await typeInto(byTestId('smtp-port'), '70000')
    await click(byTestId('email-save'))
    expect(h.wrapper.text()).toContain('Enter a port between 1 and 65535')
    expect(h.calls('PUT /admin/email')).toHaveLength(0)
  })

  it('an empty host blocks both Save and Send test', async () => {
    const h = await mountPage(EMAIL, { 'PUT /admin/email': json(EMAIL), 'POST /admin/email/test': json({ ok: true }) })
    await h.flush()
    await typeInto(byTestId('smtp-host'), '')
    await click(byTestId('email-save'))
    await click(byTestId('email-test'))
    await h.flush()
    expect(h.calls(/^(PUT|POST)/)).toHaveLength(0)
    expect(byTestId('email-test-form')).toBeNull()
    expect(h.wrapper.text()).toContain('This field is required')
  })

  it('sends the unsaved form as the test config and explains an auth failure', async () => {
    const h = await mountPage(EMAIL, {
      'POST /admin/email/test': json({ ok: false, error_code: 'auth_failed', message: '535 5.7.8 Authentication failed' }),
    })
    await h.flush()
    await typeInto(byTestId('smtp-host'), 'mail.example.org')
    await click(byTestId('email-test'))
    await h.flush()
    expect(byTestId('email-test-form')).toBeTruthy()
    await typeInto(byTestId('email-test-to'), 'me@example.com')
    await click(byTestId('email-test-send'))
    await h.flush()
    const body = h.calls('POST /admin/email/test')[0]!.body as any
    expect(body.to).toBe('me@example.com')
    expect(body.config.smtp.host).toBe('mail.example.org')
    expect(h.calls('PUT /admin/email')).toHaveLength(0)
    expect(byTestId('email-test-failure')!.textContent).toContain('The mail server refused the user name or password.')
  })

  it('shows auth templates locked on and opens a sandboxed template preview', async () => {
    const h = await mountPage()
    await h.flush()
    expect(input('template-toggle-auth.reset_password').disabled).toBe(true)
    expect(input('template-toggle-auth.reset_password').checked).toBe(true)
    expect(input('template-toggle-staff.sla').checked).toBe(false)
    await click(byTestId('template-preview-customer.reply'))
    const iframe = byTestId('template-preview-iframe') as HTMLIFrameElement
    expect(iframe.getAttribute('src')).toContain('/admin/email/templates/customer.reply/preview')
    expect(iframe.getAttribute('sandbox')).toBe('')
  })
})

describe('Email forms (FE-005)', () => {
  it('EmailSettingsForm: an empty submit sends nothing', async () => {
    const h = await mountAgent(EmailSettingsForm, { props: { settings: null } })
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.wrapper.text()).toContain('This field is required')
    expect(h.calls()).toHaveLength(0)
  })
  it('EmailTestForm: an empty recipient sends nothing', async () => {
    const h = await mountAgent(EmailTestForm, { me: null, props: { config: null } })
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.wrapper.text()).toContain('This field is required')
    expect(h.calls()).toHaveLength(0)
  })
})
