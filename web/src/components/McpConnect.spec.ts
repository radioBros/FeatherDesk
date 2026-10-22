// SPDX-License-Identifier: AGPL-3.0-only

import McpConnect from './McpConnect.vue'
import { mountAgent, click } from '@/areas/agent/testing'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const info = {
  enabled: true,
  endpoint: 'https://help.example.test/mcp',
  protocol_versions: ['2026-07-28', '2025-11-25'],
  recommended_scopes: ['tickets:read', 'tickets:write', 'kb:read'],
  read_only_scopes: ['tickets:read', 'kb:read'],
}

describe('McpConnect (MCP-010)', () => {
  it('shows the endpoint, the scopes and a config per client, never a real token', async () => {
    const h = await mountAgent(McpConnect, { api: { 'GET /integrations/mcp': info } })
    await h.flush()
    const panel = h.wrapper.find('[data-testid="mcp-connect"]')
    expect(panel.exists()).toBe(true)
    await click(panel.find('button').element)
    await h.flush()
    expect(h.wrapper.find('[data-testid="mcp-endpoint"]').text()).toBe('https://help.example.test/mcp')
    expect(h.wrapper.find('[data-testid="mcp-scopes"]').text()).toContain('tickets:write')
    const snippet = () => h.wrapper.find('[data-testid="mcp-snippet"]').text()
    expect(snippet()).toBe('claude mcp add --transport http featherdesk https://help.example.test/mcp --header "Authorization: Bearer fd_pat_…"')

    await click(h.wrapper.find('[data-testid="mcp-tab-cursor"]').element)
    await h.flush()
    expect(JSON.parse(snippet())).toEqual({ mcpServers: { featherdesk: { url: 'https://help.example.test/mcp', headers: { Authorization: 'Bearer fd_pat_…' } } } })

    await click(h.wrapper.find('[data-testid="mcp-tab-claudeDesktop"]').element)
    await h.flush()
    const desktop = JSON.parse(snippet())
    expect(desktop.mcpServers.featherdesk.command).toBe('npx')
    expect(desktop.mcpServers.featherdesk.args).toEqual(['-y', 'mcp-remote', 'https://help.example.test/mcp', '--header', 'Authorization:Bearer ${FD_TOKEN}'])
  })

  it('stays hidden while MCP is switched off', async () => {
    const h = await mountAgent(McpConnect, { api: { 'GET /integrations/mcp': { ...info, enabled: false } } })
    await h.flush()
    expect(h.wrapper.find('[data-testid="mcp-connect"]').exists()).toBe(false)
  })
})
