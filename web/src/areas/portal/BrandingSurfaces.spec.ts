// SPDX-License-Identifier: AGPL-3.0-only

import { defineComponent, h } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { createPinia, setActivePinia } from 'pinia'
import PortalShell from '@/components/PortalShell.vue'
import PortalLoginPage from './PortalLoginPage.vue'
import { requireEmail, requireRegistration } from './routeGuards'
import { mockFetch } from '@/test/mount'
import { mergeConfig, useConfigStore, DEFAULT_BRANDING } from '@/stores/config'
import { mountPage } from './testing'

afterEach(() => vi.unstubAllGlobals())

const Shell = defineComponent({ render: () => h(PortalShell, null, { default: () => h('div') }) })

describe('branding surfaces (FE-050)', () => {
  it('the footer renders only the configured legal links and the company name', async () => {
    mockFetch()
    const { wrapper } = await mountPage(Shell, {
      path: '/x',
      config: { branding: { ...DEFAULT_BRANDING, company_name: 'ACME', privacy_url: 'https://acme.test/privacy', terms_url: null, imprint_url: 'https://acme.test/imprint' } },
    })
    const footer = wrapper.find('[data-testid="portal-footer"]')
    const hrefs = footer.findAll('a').map((a) => a.attributes('href'))
    expect(hrefs).toContain('https://acme.test/privacy')
    expect(hrefs).toContain('https://acme.test/imprint')
    expect(footer.text()).not.toContain('Terms')
    expect(footer.text()).toContain('ACME')
  })

  it('the "Powered by" link disappears when toggled off', async () => {
    mockFetch()
    const on = await mountPage(Shell, { path: '/x', config: { branding: { ...DEFAULT_BRANDING, powered_by: true } } })
    expect(on.wrapper.find('[data-testid="powered-by"]').exists()).toBe(true)
    on.wrapper.unmount()
    const off = await mountPage(Shell, { path: '/x', config: { branding: { ...DEFAULT_BRANDING, powered_by: false } } })
    expect(off.wrapper.find('[data-testid="powered-by"]').exists()).toBe(false)
  })

  it('the login page shows the configured title, subtitle and background', async () => {
    mockFetch()
    const { wrapper } = await mountPage(PortalLoginPage, {
      path: '/login',
      me: null,
      config: { branding: { ...DEFAULT_BRANDING, login_title: 'ACME Support', login_subtitle: 'We are here to help', login_background_color: '#112233' } },
    })
    expect(wrapper.find('[data-testid="auth-title"]').text()).toBe('ACME Support')
    expect(wrapper.find('[data-testid="auth-subtitle"]').text()).toBe('We are here to help')
    expect(wrapper.find('[data-testid="auth-background"]').attributes('style')).toContain('background-color')
  })

  it('with email_enabled=false the Register and Forgot links are absent', async () => {
    mockFetch()
    const { wrapper } = await mountPage(PortalLoginPage, { path: '/login', me: null, config: { email_enabled: false } })
    expect(wrapper.find('[data-testid="register-link"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="forgot-link"]').exists()).toBe(false)
  })

  it('with email_enabled=false /register and /forgot-password redirect to /login', async () => {
    setActivePinia(createPinia())
    const config = useConfigStore()
    const routes = [
      { path: '/login', component: { render: () => h('div', 'login') } },
      { path: '/register', beforeEnter: requireRegistration, component: { render: () => h('div', 'register') } },
      { path: '/forgot-password', beforeEnter: requireEmail, component: { render: () => h('div', 'forgot') } },
    ]
    const router = createRouter({ history: createMemoryHistory(), routes })
    config.config = mergeConfig({ email_enabled: false })
    await router.push('/register')
    expect(router.currentRoute.value.path).toBe('/login')
    await router.push('/forgot-password')
    expect(router.currentRoute.value.path).toBe('/login')
    config.config = mergeConfig({ email_enabled: true })
    await router.push('/register')
    expect(router.currentRoute.value.path).toBe('/register')
    config.config = mergeConfig({ email_enabled: true, customer: { local: true, oidc: false, self_registration: false } })
    await router.push('/forgot-password')
    await router.push('/register')
    expect(router.currentRoute.value.path).toBe('/login')
  })
})
