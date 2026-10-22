// SPDX-License-Identifier: AGPL-3.0-only

import { cfFromString, cfToString, filtersFromQuery, toApiParams, toRouteQuery } from './query'

describe('ticket list query mapping (FE-021, FE-054)', () => {
  it('round-trips URL query ⇄ filters', () => {
    const q = { status: 'open,pending', tier: '2', 'cf.plan': 'gold', sort: 'due', view: 'mine' }
    const f = filtersFromQuery(q)
    expect(f).toMatchObject({ status: ['open', 'pending'], tier: '2', cf: { plan: 'gold' }, sort: 'due', view: 'mine' })
    expect(toRouteQuery(f)).toEqual(q)
  })
  it('explicit filters win over the queue view; default sort is attention', () => {
    const p = toApiParams(filtersFromQuery({ view: 'mine', assignee: 'u-7' }))
    expect(p.get('assignee')).toBe('u-7')
    expect(p.get('sort')).toBe('attention')
    expect(toApiParams(filtersFromQuery({ view: 'unassigned' })).get('assignee')).toBe('none')
    expect(toApiParams(filtersFromQuery({ view: 'all' })).has('assignee')).toBe(false)
  })
  it('ignores unknown views and empty values', () => {
    const f = filtersFromQuery({ view: 'hack', q: '', status: '' })
    expect(f.view).toBeUndefined()
    expect([...toApiParams(f).keys()]).toEqual(['sort', 'limit'])
  })
  it('serialises typed custom-field values', () => {
    expect(cfToString(['a', 'b'])).toBe('a,b')
    expect(cfToString(true)).toBe('true')
    expect(cfToString(false)).toBe('')
    expect(cfToString(3)).toBe('3')
    expect(cfFromString('multiselect', 'a,b')).toEqual(['a', 'b'])
    expect(cfFromString('number', '3')).toBe(3)
    expect(cfFromString('checkbox', 'true')).toBe(true)
    expect(cfFromString('select', undefined)).toBeNull()
  })
})
