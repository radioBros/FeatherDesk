// SPDX-License-Identifier: AGPL-3.0-only

import CalendarsPage from './CalendarsPage.vue'
import CalendarForm from './CalendarForm.vue'
import { nextBusinessMinute, timeZones } from './calendar'
import { mountAgent, fx, json, byTestId, typeInto, click } from '../testing'
import type { components } from '@/api/schema'

type S = components['schemas']
const cal = (extra: Partial<S['BusinessCalendar']> = {}): S['BusinessCalendar'] => ({
  id: 'cal-1',
  name: 'Rome office',
  timezone: 'Europe/Rome',
  week: { mon: [{ start: '09:00', end: '17:00' }], tue: [{ start: '09:00', end: '17:00' }] },
  holidays: ['2026-12-25'],
  ...extra,
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  document.body.innerHTML = ''
})

describe('CalendarsPage (FE-056)', () => {
  it('lists calendars', async () => {
    const h = await mountAgent(CalendarsPage, { me: fx.me('supervisor'), api: { 'GET /admin/calendars': [cal()] } })
    const row = h.wrapper.find('[data-testid="calendar-row"]')
    expect(row.text()).toContain('Rome office')
    expect(row.text()).toContain('Europe/Rome')
  })
})

describe('CalendarForm (FE-056, FE-005)', () => {
  it('an empty name sends nothing', async () => {
    const h = await mountAgent(CalendarForm, {})
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.wrapper.text()).toContain('This field is required')
    expect(h.calls()).toHaveLength(0)
  })

  it('Mon 09:00–12:00 + 11:00–14:00 is rejected with the time_range_order message', async () => {
    const h = await mountAgent(CalendarForm, {
      props: { calendar: cal({ week: { mon: [{ start: '09:00', end: '12:00' }, { start: '11:00', end: '14:00' }] } }) },
    })
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(byTestId('calendar-day-mon')!.textContent).toContain('The end must be after the start and ranges must not overlap')
    expect(h.calls()).toHaveLength(0)
  })

  it('an end before the start is rejected', async () => {
    const h = await mountAgent(CalendarForm, { props: { calendar: cal() } })
    await typeInto(byTestId('calendar-mon-end-0'), '08:00')
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(byTestId('calendar-day-mon')!.textContent).toContain('The end must be after the start')
    expect(h.calls()).toHaveLength(0)
  })

  it('an invalid zone and duplicate holidays are rejected', async () => {
    const h = await mountAgent(CalendarForm, { props: { calendar: cal({ timezone: 'Mars/Olympus', holidays: ['2026-12-25', '2026-12-25'] }) } })
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.wrapper.text()).toContain('Choose a valid time zone')
    expect(h.wrapper.text()).toContain('This value is used more than once')
    expect(h.calls()).toHaveLength(0)
  })

  it('the zone list comes from Intl (no network)', async () => {
    const spy = vi.spyOn(Intl as unknown as { supportedValuesOf: (k: string) => string[] }, 'supportedValuesOf')
    const h = await mountAgent(CalendarForm, {})
    expect(spy).toHaveBeenCalledWith('timeZone')
    expect(timeZones()).toContain('Europe/Rome')
    expect(h.calls()).toHaveLength(0)
  })

  it('saves the week, sorted holidays and the zone', async () => {
    const h = await mountAgent(CalendarForm, {
      props: { calendar: cal({ holidays: ['2026-12-26', '2026-12-25'] }) },
      api: { 'PATCH /admin/calendars/cal-1': (_u, init) => json({ ...cal(), ...init.body }) },
    })
    await click(byTestId('calendar-save'))
    const body = h.calls('PATCH /admin/calendars/cal-1')[0]!.body as S['CalendarCreate']
    expect(body.timezone).toBe('Europe/Rome')
    expect(body.holidays).toEqual(['2026-12-25', '2026-12-26'])
    expect(body.week.mon).toEqual([{ start: '09:00', end: '17:00' }])
    expect(body.week.sat).toEqual([])
    expect(byTestId('calendar-preview')!.textContent).toContain('Next business minute')
  })
})

describe('nextBusinessMinute', () => {
  const week = { mon: [{ start: '09:00', end: '17:00' }], tue: [{ start: '09:00', end: '17:00' }] }
  it('returns the same minute inside business hours', () => {
    // Monday 2026-10-19 10:15 in Rome = 08:15 UTC
    const from = new Date('2026-10-19T08:15:00Z')
    expect(nextBusinessMinute({ timezone: 'Europe/Rome', week }, from)!.toISOString()).toBe('2026-10-19T08:15:00.000Z')
  })
  it('jumps to the next opening, skipping holidays', () => {
    // Monday 18:00 Rome → Tuesday is a holiday → next Monday 09:00 Rome (08:00 UTC, after the DST change)
    const from = new Date('2026-10-19T16:00:00Z')
    const next = nextBusinessMinute({ timezone: 'Europe/Rome', week, holidays: ['2026-10-20'] }, from)
    expect(next!.toISOString()).toBe('2026-10-26T08:00:00.000Z')
  })
  it('returns null without business hours', () => {
    expect(nextBusinessMinute({ timezone: 'UTC', week: {} })).toBeNull()
  })
})
