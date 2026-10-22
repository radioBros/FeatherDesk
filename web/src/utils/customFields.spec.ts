// SPDX-License-Identifier: AGPL-3.0-only

import { dropFieldValues, emptyFieldValue, fieldApplies } from './customFields'

describe('fieldApplies (mirrors FieldDef.AppliesTo)', () => {
  const billingOnly = { category_ids: ['billing'], project_ids: null }
  it('a category-scoped field applies only in its categories, never without one', () => {
    expect(fieldApplies(billingOnly, null)).toBe(false)
    expect(fieldApplies(billingOnly, 'tech')).toBe(false)
    expect(fieldApplies(billingOnly, 'billing')).toBe(true)
  })
  it('null or empty scopes mean every category and project', () => {
    expect(fieldApplies({ category_ids: null, project_ids: null }, null, 'p1')).toBe(true)
    expect(fieldApplies({ category_ids: [], project_ids: [] }, 'x', 'p1')).toBe(true)
  })
  it('a project-scoped field applies only in its projects; an unknown project does not filter', () => {
    const mob = { category_ids: null, project_ids: ['mob'] }
    expect(fieldApplies(mob, null, 'web')).toBe(false)
    expect(fieldApplies(mob, null, 'mob')).toBe(true)
    expect(fieldApplies(mob, null, null)).toBe(true)
  })
})

describe('emptyFieldValue / dropFieldValues', () => {
  it('treats null, blank strings and empty lists as empty', () => {
    for (const v of [undefined, null, '', '  ', []]) expect(emptyFieldValue(v)).toBe(true)
    for (const v of ['x', 0, false, ['a']]) expect(emptyFieldValue(v)).toBe(false)
  })
  it('drops the keys that are not kept', () => {
    const values: Record<string, unknown> = { a: 1, b: 2 }
    dropFieldValues(values, ['a'])
    expect(values).toEqual({ a: 1 })
  })
})
