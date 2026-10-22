// SPDX-License-Identifier: AGPL-3.0-only

import { defineComponent, nextTick } from 'vue'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createAppVuetify } from '@/plugins/vuetify'
import { i18n } from '@/i18n'
import { useTableColumns, type ColumnDef } from '@/composables/useTableColumns'
import ColumnCells from './ColumnCells.vue'
import ColumnsMenu from './ColumnsMenu.vue'

const defs: ColumnDef[] = [
  { key: 'name', label: 'Name', locked: true },
  { key: 'email', label: 'Email' },
  { key: 'status', label: 'Status' },
]

function harness() {
  const cols = useTableColumns('spec', defs, { mobile: () => false, userId: () => 'u' })
  const Table = defineComponent({
    components: { ColumnCells },
    setup: () => ({ cols, rows: [{ id: 1 }] }),
    template: `<table>
      <thead><tr><ColumnCells :cols="cols" head>
        <th class="sel">#</th>
        <th v-if="cols.show('name')" data-col="name">Name</th>
        <th v-if="cols.show('email')" data-col="email">Email</th>
        <th v-if="cols.show('status')" data-col="status" style="color: red">Status</th>
        <th class="act">Actions</th>
      </ColumnCells></tr></thead>
      <tbody><tr v-for="r in rows" :key="r.id"><ColumnCells :cols="cols">
        <td class="sel">x</td>
        <td v-if="cols.show('name')" data-col="name">n</td>
        <td v-if="cols.show('email')" data-col="email">e</td>
        <td v-if="cols.show('status')" data-col="status">s</td>
        <td class="act">a</td>
      </ColumnCells></tr></tbody>
    </table>`,
  })
  const w = mount(Table, { global: { plugins: [createAppVuetify(), i18n] }, attachTo: document.body })
  const head = () => w.findAll('th').map((c) => c.attributes('data-col') ?? c.classes()[0])
  const body = () => w.findAll('td').map((c) => c.attributes('data-col') ?? c.classes()[0])
  return { cols, w, head, body }
}

describe('ColumnCells', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
  })

  it('renders cells in the user order, keeping undeclared cells in place', async () => {
    const { cols, w, head, body } = harness()
    expect(head()).toEqual(['sel', 'name', 'email', 'status', 'act'])
    cols.move('status', 0)
    cols.set('email', false)
    await nextTick()
    expect(head()).toEqual(['sel', 'status', 'name', 'act'])
    expect(body()).toEqual(['sel', 'status', 'name', 'act'])
    w.unmount()
  })

  it('adds a resizer to declared header cells and fixes the table layout once sized', async () => {
    const { cols, w } = harness()
    expect(w.findAll('[data-resize-col]').map((r) => r.attributes('data-resize-col'))).toEqual(['name', 'email', 'status'])
    expect(w.find('table').attributes('style') ?? '').not.toContain('fixed')
    cols.setWidths({ ':0': 50, name: 200, email: 120, status: 90, ':1': 100 })
    await nextTick()
    const table = w.find('table').element as HTMLTableElement
    expect(table.style.tableLayout).toBe('fixed')
    expect(table.style.width).toBe('560px')
    const status = w.find('th[data-col="status"]').element as HTMLElement
    expect(status.style.width).toBe('90px')
    expect(status.style.color).toBe('red')
    cols.reset()
    await nextTick()
    expect(table.style.tableLayout).toBe('')
    w.unmount()
  })

  it('resizes with the arrow keys on the focused handle', async () => {
    const { cols, w } = harness()
    await w.find('[data-resize-col="email"]').trigger('keydown', { key: 'ArrowRight' })
    expect(cols.sized.value).toBe(true)
    const before = cols.width('email')!
    await w.find('[data-resize-col="email"]').trigger('keydown', { key: 'ArrowLeft' })
    expect(cols.width('email')).toBe(Math.max(48, before - 16))
    w.unmount()
  })
})

describe('ColumnsMenu', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
  })

  it('moves a column with the arrow keys on its drag handle', async () => {
    const cols = useTableColumns('menu', defs, { mobile: () => false, userId: () => 'u' })
    const w = mount(ColumnsMenu, { props: { cols }, global: { plugins: [createAppVuetify(), i18n] }, attachTo: document.body })
    await w.find('[data-testid="columns-menu"]').trigger('click')
    await nextTick()
    const handle = document.querySelector<HTMLElement>('[data-testid="column-drag-status"]')!
    handle.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true }))
    await nextTick()
    expect(cols.all.value.map((c) => c.key)).toEqual(['name', 'status', 'email'])
    const items = Array.from(document.querySelectorAll<HTMLElement>('[data-col-item]')).map((e) => e.dataset.colItem)
    expect(items).toEqual(['name', 'status', 'email'])
    w.unmount()
  })
})
