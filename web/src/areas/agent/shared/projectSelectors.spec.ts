// SPDX-License-Identifier: AGPL-3.0-only

/** Project selectors on the configuration forms and project columns of their lists (D-20). */
import ProjectSelect from '@/components/ProjectSelect.vue'
import ProjectMultiSelect from '@/components/ProjectMultiSelect.vue'
import CategoryForm from '../directory/admin/CategoryForm.vue'
import CategoriesPage from '../directory/admin/CategoriesPage.vue'
import TeamForm from '../directory/admin/TeamForm.vue'
import StaffForm from '../directory/admin/StaffForm.vue'
import WebhookForm from '../config/WebhookForm.vue'
import CustomFieldForm from '../config/CustomFieldForm.vue'
import RuleForm from '../automation/RuleForm.vue'
import KbCategoryForm from '../content/KbCategoryForm.vue'
import ProjectCell from './ProjectCell.vue'
import OrganizationForm from '../directory/OrganizationForm.vue'
import { mountAgent, fx, json, byTestId, typeInto, click, type AgentHarness } from '../testing'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const web = { id: 'p-web', key: 'WEB', name: 'Web App', color: '#1976d2' }
const mob = { id: 'p-mob', key: 'MOB', name: 'Mobile', color: '#e64a19' }
const projects = [web, mob]

function select(h: AgentHarness, testid: string, index = 0) {
  const all = h.wrapper.findAllComponents({ name: 'AutocompleteSearch' }).filter((c) => c.attributes('data-testid') === testid)
  const c = all[index]
  if (!c) throw new Error(`no select ${testid}[${index}]`)
  return c
}
async function choose(h: AgentHarness, testid: string, value: unknown, index = 0) {
  select(h, testid, index).vm.$emit('update:modelValue', value)
  await h.flush()
}

describe('ProjectSelect', () => {
  it('offers the global choice only when nullable and maps it to null', async () => {
    const h = await mountAgent(ProjectSelect, { props: { modelValue: 'p-web', projects, label: 'Project', nullable: true, nullLabel: 'All', testid: 'ps' } })
    const sel = select(h, 'ps')
    expect((sel.props('items') as { id: string }[]).map((i) => i.id)).toEqual(['__global__', 'p-web', 'p-mob'])
    await choose(h, 'ps', '__global__')
    expect(h.wrapper.emitted('update:modelValue')![0]).toEqual([null])
    await choose(h, 'ps', 'p-mob')
    expect(h.wrapper.emitted('update:modelValue')![1]).toEqual(['p-mob'])
  })
  it('has no global choice when required', async () => {
    const h = await mountAgent(ProjectSelect, { props: { modelValue: null, projects, label: 'Project', testid: 'ps' } })
    expect((select(h, 'ps').props('items') as unknown[]).length).toBe(2)
  })
})

describe('ProjectMultiSelect', () => {
  it('emits the chosen ids', async () => {
    const h = await mountAgent(ProjectMultiSelect, { props: { modelValue: [], projects, label: 'Projects', testid: 'pm' } })
    await choose(h, 'pm', ['p-web', 'p-mob'])
    expect(h.wrapper.emitted('update:modelValue')![0]).toEqual([['p-web', 'p-mob']])
  })
})

describe('CategoryForm project', () => {
  it('sends the chosen project and is required', async () => {
    const h = await mountAgent(CategoryForm, {
      props: { teams: [], projects: [web, mob] },
      api: { 'POST /admin/categories': (_u, init) => json({ id: 'c-9', sort: 1, active: true, ...init.body }, 201) },
    })
    await typeInto(byTestId('category-name'), 'Billing')
    await choose(h, 'category-project', 'p-mob')
    await click(byTestId('category-save'))
    expect(h.calls('POST /admin/categories')[0]!.body).toMatchObject({ name: 'Billing', project_id: 'p-mob' })
  })
  it('does not resend the project of an existing category', async () => {
    const h = await mountAgent(CategoryForm, {
      props: { teams: [], projects, category: fx.category({ project_id: 'p-web' }) },
      api: { 'PATCH /admin/categories/cat-1': (_u, init) => json({ ...fx.category(), ...init.body }) },
    })
    await click(byTestId('category-save'))
    expect(h.calls('PATCH /admin/categories/cat-1')[0]!.body).not.toHaveProperty('project_id')
  })
})

describe('CategoriesPage project column and filter', () => {
  it('shows the project chip, filters by project and can hide the column', async () => {
    const h = await mountAgent(CategoriesPage, {
      me: fx.me('supervisor'),
      api: {
        'GET /admin/categories': [fx.category({ id: 'a', name: 'Alpha', project_id: 'p-web' }), fx.category({ id: 'b', name: 'Beta', project_id: 'p-mob' })],
        'GET /agent/teams': [],
        'GET /agent/projects': [fx.project(), fx.project({ id: 'p-mob', key: 'MOB', name: 'Mobile' })],
      },
    })
    await h.flush()
    const rows = () => h.wrapper.findAll('[data-testid="category-row"]')
    expect(rows()).toHaveLength(2)
    expect(rows()[0]!.text()).toContain('WEB')
    expect(rows()[1]!.text()).toContain('MOB')
    await choose(h, 'category-project-filter', 'p-mob')
    expect(rows()).toHaveLength(1)
    expect(rows()[0]!.text()).toContain('Beta')
    await h.wrapper.find('[data-testid="columns-menu"]').trigger('click')
    await h.flush()
    expect(document.body.querySelector('[data-testid="column-toggle-project"]')).not.toBeNull()
  })
})

describe('TeamForm, WebhookForm, CustomFieldForm, OrganizationForm: project_ids', () => {
  it('team: sends project_ids only when chosen', async () => {
    const h = await mountAgent(TeamForm, {
      props: { calendars: [], projects },
      api: { 'POST /admin/teams': (_u, init) => json({ id: 't-9', ...init.body }, 201) },
    })
    await typeInto(byTestId('team-name'), 'Mobile team')
    await choose(h, 'team-projects', ['p-mob'])
    await click(byTestId('team-save'))
    expect(h.calls('POST /admin/teams')[0]!.body).toMatchObject({ project_ids: ['p-mob'] })
  })
  it('team: omits project_ids when none chosen (default project)', async () => {
    const h = await mountAgent(TeamForm, { props: { calendars: [], projects }, api: { 'POST /admin/teams': (_u, init) => json({ id: 't-9', ...init.body }, 201) } })
    await typeInto(byTestId('team-name'), 'Plain')
    await click(byTestId('team-save'))
    expect(h.calls('POST /admin/teams')[0]!.body).not.toHaveProperty('project_ids')
  })
  it('webhook form lists the projects selector', async () => {
    const h = await mountAgent(WebhookForm, { props: { projects } })
    expect(select(h, 'webhook-projects').exists()).toBe(true)
  })
  it('custom field form lists the projects selector', async () => {
    const h = await mountAgent(CustomFieldForm, { props: { projects } })
    expect(select(h, 'cf-projects').exists()).toBe(true)
  })
  it('organization: sends the allowed projects', async () => {
    const h = await mountAgent(OrganizationForm, {
      props: { projects },
      api: { 'POST /agent/organizations': (_u, init) => json({ id: 'o-9', status: 'active', ...init.body }, 201) },
    })
    await typeInto(byTestId('org-name'), 'Globex')
    await choose(h, 'org-projects', ['p-web'])
    await click(byTestId('org-save'))
    expect(h.calls('POST /agent/organizations')[0]!.body).toMatchObject({ project_ids: ['p-web'] })
  })
})

describe('StaffForm memberships editor', () => {
  it('adds a membership with a role and sends it on create', async () => {
    const h = await mountAgent(StaffForm, {
      props: { teams: [], projects },
      api: { 'POST /admin/staff': (_u, init) => json({ id: 'u-9', ...init.body }, 201) },
    })
    expect(h.wrapper.text()).toContain('No project yet')
    await click(byTestId('staff-membership-add'))
    expect(h.wrapper.findAll('[data-testid="staff-membership"]')).toHaveLength(1)
    await choose(h, 'staff-membership-role', 'project_admin')
    await typeInto(byTestId('staff-name'), 'New Agent')
    await typeInto(byTestId('staff-email'), 'new@example.com')
    await click(byTestId('staff-save'))
    expect(h.calls('POST /admin/staff')[0]!.body).toMatchObject({ projects: [{ project_id: 'p-web', role: 'project_admin' }] })
  })
  it('on edit sends the list only when it changed, and removing a row works', async () => {
    const staff = fx.user({ id: 'u-2', kind: 'staff', role: 'agent', projects: [{ project_id: 'p-web', role: 'member' }] })
    const h = await mountAgent(StaffForm, {
      props: { teams: [], projects, staff },
      api: { 'PATCH /admin/staff/u-2': (_u, init) => json({ ...staff, ...init.body }) },
    })
    await click(byTestId('staff-save'))
    expect(h.calls('PATCH /admin/staff/u-2')[0]!.body).not.toHaveProperty('projects')
    await click(byTestId('staff-membership-remove'))
    await click(byTestId('staff-save'))
    expect(h.calls('PATCH /admin/staff/u-2')[1]!.body).toMatchObject({ projects: [] })
  })
})

describe('RuleForm, KbCategoryForm, ProjectCell', () => {
  it('rule: scopes to a project and offers the project condition field', async () => {
    const h = await mountAgent(RuleForm, {
      props: { projects, canGlobal: true, teams: [], categories: [], staff: [], canned: [], customFields: [], organizations: [] },
      api: { 'POST /admin/automation/rules': (_u, init) => json({ id: 'r-9', position: 1, ...init.body }, 201) },
    })
    expect(select(h, 'rule-project').exists()).toBe(true)
    await choose(h, 'rule-project', 'p-mob')
    expect(h.wrapper.findComponent({ name: 'AutocompleteSearch', props: { modelValue: 'p-mob' } }).exists()).toBe(true)
  })
  it('kb category: a project admin cannot leave the project empty', async () => {
    const h = await mountAgent(KbCategoryForm, { props: { projects: [web], canGlobal: false } })
    expect((select(h, 'kbcat-project').props('items') as { id: string }[]).map((i) => i.id)).toEqual(['p-web'])
    await h.wrapper.find('form').trigger('submit')
    await h.flush()
    expect(h.calls()).toHaveLength(0)
  })
  it('project cell shows chips, or the empty label for every project', async () => {
    const h = await mountAgent(ProjectCell, { props: { ids: [] }, api: {} })
    expect(h.wrapper.text()).toContain('All projects')
    const h2 = await mountAgent(ProjectCell, { props: { ids: ['p-x'] } })
    expect(h2.wrapper.findAll('[data-testid="project-chip"]')).toHaveLength(1)
  })
})
