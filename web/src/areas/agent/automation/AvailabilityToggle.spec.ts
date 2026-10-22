// SPDX-License-Identifier: AGPL-3.0-only

import { defineComponent, h as hFn } from 'vue'
import AvailabilityToggle from './AvailabilityToggle.vue'
import TeamForm from '../directory/admin/TeamForm.vue'
import TimelineTab from '../tickets/detail/TimelineTab.vue'
import UserMenu from '@/components/UserMenu.vue'
import { eventActor, eventDetail, eventLabel } from '../tickets/detail/events'
import { mountAgent, fx, json, byTestId, allByTestId, click, type AgentHarness } from '../testing'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

/** The toggle inside a list, as the agent layout renders it in the user menu. */
const InMenu = defineComponent({ render: () => hFn('div', { class: 'v-list' }, [hFn(AvailabilityToggle)]) })

describe('AvailabilityToggle (FE-060)', () => {
  it('shows the current state and PATCHes away: true', async () => {
    const h = await mountAgent(InMenu, {
      api: {
        'GET /me/availability': { away: false, away_since: null },
        'PATCH /me/availability': (_u, init) => json({ away: (init.body as { away: boolean }).away, away_since: fx.iso }),
      },
    })
    await h.flush()
    const sw = h.wrapper.findComponent({ name: 'VSwitch' })
    expect(sw.props('modelValue')).toBe(false)
    expect(h.wrapper.text()).toContain('You receive automatic assignments.')
    sw.vm.$emit('update:modelValue', true)
    await h.flush()
    expect(h.calls('PATCH /me/availability')[0]!.body).toEqual({ away: true })
    expect(h.wrapper.findComponent({ name: 'VSwitch' }).props('modelValue')).toBe(true)
    expect(h.wrapper.text()).toContain('You receive no automatic assignments.')
    expect(byTestId('nav-my-macros')).not.toBeNull()
  })

  it('renders nothing for customers and never calls the API', async () => {
    const h = await mountAgent(InMenu, { me: fx.me('agent', 1, { kind: 'customer' } as never) })
    expect(byTestId('availability')).toBeNull()
    expect(h.calls()).toHaveLength(0)
  })

  it('the shared UserMenu has no availability entry without the agent slot (portal)', async () => {
    const h = await mountAgent(UserMenu, { props: { area: 'portal' } })
    await click(byTestId('user-menu'))
    await h.flush()
    expect(byTestId('sign-out')).not.toBeNull()
    expect(byTestId('availability')).toBeNull()
  })
})

describe('TeamForm assignment strategy (FE-061)', () => {
  async function save(h: AgentHarness) {
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
  }
  it('always sends assignment_strategy (default manual)', async () => {
    let body: Record<string, unknown> | null = null
    const h = await mountAgent(TeamForm, {
      props: { team: fx.team(), calendars: [] },
      api: { 'PATCH /admin/teams/team-1': (_u, init) => ((body = init.body), json(fx.team())) },
    })
    await save(h)
    expect(body).toMatchObject({ assignment_strategy: 'manual' })
  })
  it('sends the chosen strategy and shows its hint', async () => {
    let body: Record<string, unknown> | null = null
    const h = await mountAgent(TeamForm, {
      props: { team: fx.team({ assignment_strategy: 'round_robin' }), calendars: [] },
      api: { 'PATCH /admin/teams/team-1': (_u, init) => ((body = init.body), json(fx.team())) },
    })
    expect(h.wrapper.text()).toContain('Takes turns over the eligible members who are not away.')
    const sel = h.wrapper.findAllComponents({ name: 'AutocompleteSearch' }).find((c) => c.attributes('data-testid') === 'team-strategy')!
    sel.vm.$emit('update:modelValue', 'least_busy')
    await h.flush()
    await save(h)
    expect(body).toMatchObject({ assignment_strategy: 'least_busy' })
  })
})

describe('Timeline: ticket.automation_applied (FE-066)', () => {
  const applied = {
    id: 'e-2',
    ticket_id: 't-1',
    type: 'ticket.automation_applied' as const,
    actor: null,
    created_at: fx.iso,
    data: {
      rule_id: 'r-1',
      rule_name: 'VIP routing',
      trigger: 'ticket.created',
      actions: [
        { type: 'add_tag', outcome: 'applied' },
        { type: 'assign', outcome: 'skipped', detail: 'no eligible agent' },
      ],
      truncated: true,
    },
  }

  it('labels, details and the Automation actor', () => {
    expect(eventLabel('ticket.automation_applied')).toBe('Automation rule applied')
    expect(eventActor(applied)).toBe('Automation')
    expect(eventActor({ type: 'ticket.status_changed', actor: null })).toBe('System')
    expect(eventDetail(applied)).toBe('“VIP routing”: Add tag, Assign (skipped: no eligible agent) · stopped at the 10-action limit')
  })

  it('renders in the timeline tab', async () => {
    const h = await mountAgent(TimelineTab, {
      props: { ticketId: 't-1' },
      api: { 'GET /agent/tickets/t-1/events': fx.page([applied]) },
    })
    await h.flush()
    const item = allByTestId('timeline-item')[0]!
    expect(item.textContent).toContain('Automation rule applied')
    expect(item.textContent).toContain('VIP routing')
    expect(item.textContent).toContain('Automation ·')
  })
})
