// SPDX-License-Identifier: AGPL-3.0-only

import ProjectSwitcher from './ProjectSwitcher.vue'
import { mountAgent, fx, json, byTestId, click } from './testing'
import { useProjectsStore } from '@/stores/projects'

afterEach(() => {
  document.body.innerHTML = ''
})

const WEB = fx.project()
const MOB = fx.project({ id: 'p-mob', key: 'MOB', name: 'Mobile', color: '#d81b60' })

async function mountSwitcher(list = [WEB, MOB], me = {}) {
  const h = await mountAgent(ProjectSwitcher, {
    me,
    api: { 'GET /agent/projects': () => json(list), 'PATCH /me': () => json({}) },
  })
  await useProjectsStore().load()
  await h.flush()
  return h
}

describe('ProjectSwitcher', () => {
  it('is hidden when the user has a single active project', async () => {
    const h = await mountSwitcher([WEB])
    expect(byTestId('project-switcher')).toBeNull()
    expect(h.wrapper.html()).not.toContain('project-switcher')
  })

  it('lists All projects and each project with key, and persists the choice', async () => {
    const h = await mountSwitcher()
    const btn = byTestId('project-switcher')!
    expect(btn.textContent).toContain('All projects')
    await click(btn)
    await h.flush()
    const list = byTestId('project-switcher-list')!
    expect(list.textContent).toContain('All projects')
    expect(list.textContent).toContain('WEB')
    expect(list.textContent).toContain('MOB')
    await click(byTestId('project-option-MOB'))
    await h.flush()
    const patch = h.calls('PATCH /me')[0]!
    expect(patch.body).toEqual({ current_project_id: 'p-mob' })
    expect(byTestId('project-switcher')!.textContent).toContain('MOB')
  })

  it('"All projects" clears the choice (null)', async () => {
    const h = await mountSwitcher([WEB, MOB], { current_project_id: 'p-web' })
    await click(byTestId('project-switcher'))
    await h.flush()
    await click(byTestId('project-option-all'))
    await h.flush()
    expect(h.calls('PATCH /me')[0]!.body).toEqual({ current_project_id: null })
  })

  it('keeps a 40 px minimum target through the top-bar button class', async () => {
    await mountSwitcher()
    expect(byTestId('project-switcher')!.className).toContain('v-btn')
  })
})
