// SPDX-License-Identifier: AGPL-3.0-only

import ReportsPage from './ReportsPage.vue'
import ReportsFilterForm from './ReportsFilterForm.vue'
import { formatMinutes } from './format'
import { mountAgent, fx, json, byTestId, typeInto, click } from '../testing'
import { i18n } from '@/i18n'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const report = {
  from: '2026-09-21T00:00:00Z',
  to: '2026-10-20T23:59:59Z',
  totals: { created: 120, resolved: 98, closed: 80, reopened: 4 },
  first_response_minutes: { median: 42, p90: 190 },
  resolution_minutes: { median: 1500, p90: 4000 },
  sla: { met: 90, breached: 10 },
  csat: { average: 4.36, count: 37 },
  by_tier: [
    { tier: 1, created: 100, resolved: 85, escalated_in: 0 },
    { tier: 2, created: 20, resolved: 13, escalated_in: 15 },
  ],
  by_team: [
    { team_id: 'team-1', name: 'Support', created: 110, resolved: 90 },
    { team_id: null, name: '', created: 10, resolved: 8 },
  ],
}

describe('ReportsPage (FE-043)', () => {
  it('renders the fixture summary with SVG charts', async () => {
    const h = await mountAgent(ReportsPage, { me: fx.me('supervisor'), api: { 'GET /agent/reports/summary': report } })
    await h.flush()
    const call = h.calls('GET /agent/reports/summary')[0]!
    expect(call.url.searchParams.get('from')).toMatch(/^\d{4}-\d{2}-\d{2}T00:00:00Z$/)
    expect(call.url.searchParams.get('to')).toMatch(/T23:59:59Z$/)
    expect(byTestId('reports-total-created')?.textContent).toBe('120')
    expect(byTestId('reports-frt')?.textContent).toContain('42 min')
    expect(byTestId('reports-resolution')?.textContent).toContain('1 d 1 h')
    expect(byTestId('reports-csat')?.textContent).toContain('4.4')
    // two grouped charts: 2 tiers × 2 series + 2 teams × 2 series
    expect(h.wrapper.findAll('svg [data-testid="chart-bar"]')).toHaveLength(8)
    expect(h.wrapper.findAll('[data-testid="ratio-segment"]')).toHaveLength(2)
    expect(h.wrapper.find('[data-testid="ratio-bar"]').text()).toContain('90%')
    expect(h.wrapper.find('[data-testid="reports-team-table"]').text()).toContain('Triage')
  })

  it('a range with to before from sends no second request', async () => {
    const h = await mountAgent(ReportsPage, { me: fx.me('agent'), api: { 'GET /agent/reports/summary': report } })
    await h.flush()
    expect(h.calls('GET /agent/reports/summary')).toHaveLength(1)
    await typeInto(byTestId('reports-from'), '2026-10-10')
    await typeInto(byTestId('reports-to'), '2026-10-01')
    await click(byTestId('reports-apply'))
    await h.flush()
    expect(h.wrapper.text()).toContain('The end date must not be before the start date')
    expect(h.calls('GET /agent/reports/summary')).toHaveLength(1)
  })

  it('shows the problem when the API fails', async () => {
    const h = await mountAgent(ReportsPage, { api: { 'GET /agent/reports/summary': json({ title: 'Boom' }, 500) } })
    await h.flush()
    expect(byTestId('reports-error')).not.toBeNull()
  })
})

describe('ReportsFilterForm (FE-005)', () => {
  it('an empty range shows the required message and emits nothing', async () => {
    const h = await mountAgent(ReportsFilterForm, { props: { teams: [] } })
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.wrapper.text()).toContain('This field is required')
    expect(h.wrapper.emitted('apply')).toBeUndefined()
  })
  it('emits the filter when valid', async () => {
    const h = await mountAgent(ReportsFilterForm, { props: { initial: { from: '2026-10-01', to: '2026-10-20' } } })
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.wrapper.emitted('apply')?.[0]?.[0]).toMatchObject({ from: '2026-10-01', to: '2026-10-20' })
  })
})

describe('formatMinutes', () => {
  const t = (k: string, p?: Record<string, unknown>) => i18n.global.t(k, p ?? {})
  it('formats minutes, hours and days', async () => {
    await mountAgent({ template: '<div />' })
    expect(formatMinutes(null, t)).toBe('—')
    expect(formatMinutes(45, t)).toBe('45 min')
    expect(formatMinutes(200, t)).toBe('3 h 20 min')
    expect(formatMinutes(3000, t)).toBe('2 d 2 h')
  })
})
