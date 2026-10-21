// SPDX-License-Identifier: AGPL-3.0-only

import { createPinia, setActivePinia } from 'pinia'
import { useCountsStore } from './counts'
import { useProjectsStore } from '@/stores/projects'
import { useSessionStore } from '@/stores/session'
import { fx } from './testing'
import type { Me } from '@/types'

const WEB = fx.project()
const MOB = fx.project({ id: 'p-mob', key: 'MOB' })

describe('counts store (by project)', () => {
  it('shows the totals, then the selected project from by_project', () => {
    setActivePinia(createPinia())
    useSessionStore().set(fx.me('agent', 1) as Me)
    const projects = useProjectsStore()
    projects.projects = [WEB, MOB]
    const counts = useCountsStore()
    counts.apply({
      mine: 5, unassigned: 3, triage: 1, tier: { '1': 4 },
      by_project: { 'p-web': { mine: 2, unassigned: 1, triage: 0, tier: { '1': 1 } }, 'p-mob': { mine: 3, unassigned: 2, triage: 1, tier: { '1': 3 } } },
    })
    expect(counts.counts.mine).toBe(5)
    useSessionStore().set({ ...useSessionStore().me!, current_project_id: 'p-mob' })
    expect(counts.counts.mine).toBe(3)
    expect(counts.counts.tier['1']).toBe(3)
    useSessionStore().set({ ...useSessionStore().me!, current_project_id: null })
    expect(counts.counts.unassigned).toBe(3)
  })
})
