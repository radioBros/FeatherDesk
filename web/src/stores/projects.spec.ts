// SPDX-License-Identifier: AGPL-3.0-only

import { createPinia, setActivePinia } from 'pinia'
import { mockFetch, json, flush } from '@/test/mount'
import { fx } from '@/areas/agent/testing'
import { useSessionStore } from './session'
import { useProjectsStore } from './projects'
import type { Me } from '@/types'

const WEB = fx.project()
const MOB = fx.project({ id: 'p-mob', key: 'MOB', name: 'Mobile' })

function setup(me: Partial<Me> = {}, list = [WEB, MOB]) {
  setActivePinia(createPinia())
  useSessionStore().set(fx.me('agent', 1, me) as Me)
  const m = mockFetch(async (raw, init) => {
    const method = (init.method ?? 'GET').toUpperCase()
    if (raw.includes('/agent/projects')) return json(list)
    if (raw.endsWith('/me') && method === 'PATCH') return json({})
    return json({}, 404)
  })
  return { store: useProjectsStore(), ...m }
}

describe('projects store', () => {
  it('shows the switcher only with more than one active project', async () => {
    const one = setup({}, [WEB])
    await one.store.load()
    expect(one.store.showSwitcher).toBe(false)
    const two = setup()
    await two.store.load()
    expect(two.store.showSwitcher).toBe(true)
    const inactive = setup({}, [WEB, { ...MOB, active: false }])
    await inactive.store.load()
    expect(inactive.store.showSwitcher).toBe(false)
  })

  it('filter is the saved project while the switcher is shown, else undefined', async () => {
    const { store } = setup({ current_project_id: 'p-mob' })
    await store.load()
    expect(store.filter).toBe('p-mob')
    expect(store.current?.key).toBe('MOB')
    const single = setup({ current_project_id: 'p-mob' }, [WEB])
    await single.store.load()
    expect(single.store.filter).toBeUndefined()
  })

  it('ignores a saved project the user no longer works in', async () => {
    const { store } = setup({ current_project_id: 'gone' })
    await store.load()
    expect(store.currentId).toBeNull()
  })

  it('select persists with PATCH /me and rolls back on failure', async () => {
    const { store, calls } = setup()
    await store.load()
    await store.select('p-web')
    expect(useSessionStore().me?.current_project_id).toBe('p-web')
    const patch = calls.find((c) => c.method === 'PATCH')!
    expect(JSON.parse(patch.init.body as string)).toEqual({ current_project_id: 'p-web' })
    await flush()
    mockFetch(async () => json({ title: 'no', status: 422 }, 422))
    await expect(store.select(null)).rejects.toBeTruthy()
    expect(useSessionStore().me?.current_project_id).toBe('p-web')
  })
})
