// SPDX-License-Identifier: AGPL-3.0-only

import ProjectEntryPage from './pages/ProjectEntryPage.vue'
import { mockFetch, json } from '@/test/mount'
import { mountPage, settle, CUSTOMER } from './testing'

afterEach(() => vi.unstubAllGlobals())

const PROJECT = {
  id: 'p-web', key: 'WEB', name: 'Web App', description: 'The website', color: '#1976d2', icon: '',
  logo_url: '/branding/web.png', portal_title: 'Web support', portal_intro_md: 'Tell us about **the website**',
  primary_color: '#d81b60',
}

function api(project: unknown) {
  return mockFetch((url) => (new URL(url, 'http://x').pathname.endsWith('/auth/config') ? json({ project }) : json([])))
}

describe('per-project portal entry /p/:key (D-20)', () => {
  it('asks /auth/config for the key and shows the project title, intro and logo', async () => {
    const { fn } = api(PROJECT)
    const { wrapper, config } = await mountPage(ProjectEntryPage, { path: '/p/WEB', pattern: '/p/:key', me: null })
    await settle(8)
    expect(String((fn.mock.calls[0] as unknown[])[0])).toContain('/auth/config?project=WEB')
    expect(wrapper.find('[data-testid="project-title"]').text()).toBe('Web support')
    expect(wrapper.find('[data-testid="project-intro"]').html()).toContain('<strong>the website</strong>')
    expect(wrapper.find('[data-testid="brand"] img').attributes('src')).toBe('/branding/web.png')
    expect(config.project?.key).toBe('WEB')
    expect(wrapper.find('[data-testid="project-signin"]').attributes('href')).toContain('redirect=')
    expect(decodeURIComponent(wrapper.find('[data-testid="project-signin"]').attributes('href')!)).toContain('/tickets/new?project=WEB')
  })

  it('falls back to the project name and global branding when overrides are null', async () => {
    api({ ...PROJECT, portal_title: null, portal_intro_md: null, logo_url: null, primary_color: null })
    const { wrapper } = await mountPage(ProjectEntryPage, { path: '/p/WEB', pattern: '/p/:key', me: null })
    await settle(8)
    expect(wrapper.find('[data-testid="project-title"]').text()).toBe('Web App')
    expect(wrapper.find('[data-testid="project-description"]').text()).toBe('The website')
  })

  it('shows a friendly not-found for an unknown or hidden key', async () => {
    api(null)
    const { wrapper, config } = await mountPage(ProjectEntryPage, { path: '/p/NOPE', pattern: '/p/:key', me: null })
    await settle(8)
    expect(wrapper.find('[data-testid="project-not-found"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="project-entry"]').exists()).toBe(false)
    expect(config.project).toBeNull()
  })

  it('sends a signed-in customer straight to the new-ticket form with the project', async () => {
    api(PROJECT)
    const { router } = await mountPage(ProjectEntryPage, { path: '/p/WEB', pattern: '/p/:key', me: CUSTOMER })
    await settle(8)
    expect(router.currentRoute.value.fullPath).toBe('/tickets/new?project=WEB')
  })

  it('removes the overrides when the page is left', async () => {
    api(PROJECT)
    const { wrapper, config } = await mountPage(ProjectEntryPage, { path: '/p/WEB', pattern: '/p/:key', me: null })
    await settle(8)
    expect(config.project).not.toBeNull()
    wrapper.unmount()
    expect(config.project).toBeNull()
  })
})
