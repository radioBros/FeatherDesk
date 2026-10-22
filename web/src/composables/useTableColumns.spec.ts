// SPDX-License-Identifier: AGPL-3.0-only

import { beforeEach, describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useTableColumns, type ColumnDef } from './useTableColumns'

const defs: ColumnDef[] = [
  { key: 'id', label: 'ID', locked: true },
  { key: 'name', label: 'Name', locked: true },
  { key: 'email', label: 'Email' },
  { key: 'created', label: 'Created', lowPriority: true },
  { key: 'cf.plan', label: 'Plan', defaultHidden: true },
]
const make = (mobile = false, user = 'u1', id = 't1') => useTableColumns(id, defs, { mobile: () => mobile, userId: () => user })

describe('useTableColumns', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
  })

  it('shows every column by default except defaultHidden ones', () => {
    const c = make()
    expect(c.visibleKeys.value).toEqual(['id', 'name', 'email', 'created'])
    expect(c.show('cf.plan')).toBe(false)
    expect(c.show('not-declared')).toBe(true)
    expect(c.isDefault.value).toBe(true)
  })

  it('hides low-priority columns by default on a phone', () => {
    const c = make(true)
    expect(c.visibleKeys.value).toEqual(['id', 'name', 'email'])
  })

  it('never hides locked columns', () => {
    const c = make()
    c.set('id', false)
    c.toggle('name')
    expect(c.show('id')).toBe(true)
    expect(c.show('name')).toBe(true)
    expect(localStorage.length).toBe(0)
    localStorage.setItem(c.storageKey.value, JSON.stringify({ id: false, name: false }))
    expect(make().show('id')).toBe(true)
  })

  it('persists a choice per user and per table, and restores it', () => {
    const c = make()
    c.toggle('email')
    c.toggle('cf.plan')
    expect(c.visibleKeys.value).toEqual(['id', 'name', 'created', 'cf.plan'])
    expect(JSON.parse(localStorage.getItem('fd.v1.cols.u1.t1')!)).toEqual({ email: false, 'cf.plan': true })

    const again = make()
    expect(again.visibleKeys.value).toEqual(['id', 'name', 'created', 'cf.plan'])
    expect(make(false, 'u2').show('email')).toBe(true)
    expect(make(false, 'u1', 'other').show('email')).toBe(true)
  })

  it('a stored choice wins over the phone default', () => {
    const c = make(true)
    c.set('created', true)
    expect(make(true).show('created')).toBe(true)
  })

  it('reset returns to the defaults and clears storage', () => {
    const c = make()
    c.toggle('email')
    c.reset()
    expect(c.visibleKeys.value).toEqual(['id', 'name', 'email', 'created'])
    expect(c.isDefault.value).toBe(true)
    expect(localStorage.getItem('fd.v1.cols.u1.t1')).toBeNull()
  })

  it('survives corrupt or unavailable storage', () => {
    localStorage.setItem('fd.v1.cols.u1.t1', '{not json')
    expect(make().visibleKeys.value).toHaveLength(4)
    localStorage.setItem('fd.v1.cols.u1.t1', JSON.stringify({ email: 'no', created: false }))
    expect(make().visibleKeys.value).toEqual(['id', 'name', 'email'])
    const c = make()
    const orig = Storage.prototype.setItem
    Storage.prototype.setItem = () => {
      throw new Error('denied')
    }
    try {
      c.toggle('email')
      expect(c.show('email')).toBe(false)
    } finally {
      Storage.prototype.setItem = orig
    }
  })
})
