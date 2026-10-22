// SPDX-License-Identifier: AGPL-3.0-only

import IntegrationsCard from './IntegrationsCard.vue'
import { mountAgent, fx, json, byTestId } from '../testing'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const info = { enabled: false, endpoint: 'https://help.example.test/mcp', protocol_versions: ['2026-07-28'], recommended_scopes: [], read_only_scopes: [] }

describe('IntegrationsCard (MCP-011)', () => {
  it('loads the MCP switch and saves it on toggle', async () => {
    const h = await mountAgent(IntegrationsCard, {
      me: fx.me('admin'),
      api: {
        'GET /admin/integrations': { mcp_enabled: false },
        'GET /integrations/mcp': info,
        'PATCH /admin/integrations': (_u, init) => json(init.body),
      },
    })
    await h.flush()
    expect(byTestId('integrations-card')!.textContent).toContain('AI assistants (MCP)')
    expect(byTestId('integrations-endpoint')).toBeNull()
    h.wrapper.findComponent({ name: 'VSwitch' }).vm.$emit('update:modelValue', true)
    await h.flush()
    expect(h.calls('PATCH /admin/integrations')[0]!.body).toEqual({ mcp_enabled: true })
    expect(byTestId('integrations-endpoint')!.textContent).toContain('https://help.example.test/mcp')
  })

  it('shows an error when the settings cannot be loaded', async () => {
    const h = await mountAgent(IntegrationsCard, { me: fx.me('admin'), api: {} })
    await h.flush()
    expect(h.wrapper.findComponent({ name: 'VSwitch' }).exists()).toBe(false)
    expect(byTestId('integrations-card')!.textContent).toContain('load')
  })
})
