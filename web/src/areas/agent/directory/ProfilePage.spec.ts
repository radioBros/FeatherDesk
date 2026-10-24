// SPDX-License-Identifier: AGPL-3.0-only

import ProfilePage from './ProfilePage.vue'
import PasswordForm from './PasswordForm.vue'
import SignatureForm from './SignatureForm.vue'
import ProfileForm from './ProfileForm.vue'
import { i18n, setLocale } from '@/i18n'
import MarkdownEditor from '@/components/MarkdownEditor.vue'
import { mountAgent, fx, json, byTestId, typeInto } from '../testing'

afterEach(async () => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
  await setLocale('en')
})

describe('ProfilePage (FE-059 signature, profile)', () => {
  it('loads the signature and sessions; hides the password card without a local password', async () => {
    const h = await mountAgent(ProfilePage, {
      me: { has_password: false },
      api: {
        'GET /me/signature': { signature_md: '— Alice' },
        'GET /me/sessions': [
          { id: 's-1', audience: 'staff', current: true, created_at: fx.iso, expires_at: fx.iso, user_agent: 'Firefox' },
          { id: 's-2', audience: 'staff', current: false, created_at: fx.iso, expires_at: fx.iso, user_agent: 'Safari' },
        ],
      },
    })
    await h.flush()
    expect(h.wrapper.find('[data-testid="password-card"]').exists()).toBe(false)
    expect(h.wrapper.findComponent(MarkdownEditor).props('modelValue')).toBe('— Alice')
    expect(h.wrapper.findAll('[data-testid="session-row"]')).toHaveLength(2)
    expect(h.wrapper.findAll('[data-testid="session-revoke"]')).toHaveLength(2) // the current one too: revoking it signs out (FE-037)
  })
  it('shows the password card for local accounts', async () => {
    const h = await mountAgent(ProfilePage, {
      me: { has_password: true },
      api: { 'GET /me/signature': { signature_md: '' }, 'GET /me/sessions': [] },
    })
    await h.flush()
    expect(h.wrapper.find('[data-testid="password-card"]').exists()).toBe(true)
  })
})

describe('SignatureForm (FE-059)', () => {
  it('PUTs the Markdown signature', async () => {
    const h = await mountAgent(SignatureForm, {
      props: { signature: '' },
      api: { 'PUT /me/signature': (_u, init) => json(init.body) },
    })
    await typeInto(h.wrapper.find('[data-testid="signature-form"]').element, '**Alice** · Support')
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.calls('PUT /me/signature')[0]!.body).toEqual({ signature_md: '**Alice** · Support' })
    expect(h.wrapper.emitted('saved')![0]).toEqual(['**Alice** · Support'])
  })
  it('rejects more than 2000 characters before sending', async () => {
    const h = await mountAgent(SignatureForm, { props: { signature: 'x'.repeat(2001) } })
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.wrapper.text()).toContain('Enter at most 2000 characters')
    expect(h.calls()).toHaveLength(0)
  })
})

describe('PasswordForm', () => {
  it('a mismatched confirmation sends nothing', async () => {
    const h = await mountAgent(PasswordForm)
    await typeInto(byTestId('password-current'), 'old-password-123')
    await typeInto(byTestId('password-new'), 'new-password-123')
    await typeInto(byTestId('password-confirm'), 'new-password-999')
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.wrapper.text()).toContain('The passwords do not match')
    expect(h.calls()).toHaveLength(0)
  })
  it('PATCHes /me with current and new password', async () => {
    const h = await mountAgent(PasswordForm, { api: { 'PATCH /me': fx.me() } })
    await typeInto(byTestId('password-current'), 'old-password-123')
    await typeInto(byTestId('password-new'), 'new-password-123')
    await typeInto(byTestId('password-confirm'), 'new-password-123')
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.calls('PATCH /me')[0]!.body).toEqual({ current_password: 'old-password-123', new_password: 'new-password-123' })
  })
})

describe('ProfileForm', () => {
  it('saves the name and switches the UI language', async () => {
    const h = await mountAgent(ProfileForm, {
      props: { name: 'Alice Agent', locale: 'it' },
      api: { 'PATCH /me': (_u, init) => json({ ...fx.me(), ...(init.body as object) }) },
    })
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    await h.flush()
    expect(h.calls('PATCH /me')[0]!.body).toEqual({ name: 'Alice Agent', locale: 'it' })
    await vi.waitFor(() => expect(i18n.global.locale.value).toBe('it'))
  })
})
