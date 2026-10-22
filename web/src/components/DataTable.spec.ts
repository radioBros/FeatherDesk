// SPDX-License-Identifier: AGPL-3.0-only

import { defineComponent, nextTick, ref } from 'vue'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createAppVuetify } from '@/plugins/vuetify'
import { i18n } from '@/i18n'
import { useTableColumns, type ColumnDef } from '@/composables/useTableColumns'
import { scrollTableToEnd } from '@/test/mount'
import DataTable from './DataTable.vue'

interface Row {
  id: string
  name: string
  prio: string
  rank: number
}

const rows: Row[] = [
  { id: 'a', name: 'beta', prio: 'urgent', rank: 3 },
  { id: 'b', name: 'Alpha', prio: 'low', rank: 0 },
  { id: 'c', name: 'gamma', prio: 'high', rank: 2 },
]

function harness(opts: { serverSort?: boolean; defs: ColumnDef[]; hasMore?: boolean }) {
  const cols = useTableColumns('dt-spec', opts.defs, { mobile: () => false, userId: () => 'u' })
  const sort = ref<string | null>(null)
  const events: string[] = []
  const Host = defineComponent({
    components: { DataTable },
    setup: () => ({ cols, rows, sort, opts, events }),
    template: `<DataTable :cols="cols" :items="rows" :server-sort="opts.serverSort" :sort="sort" :has-more="opts.hasMore"
      :row-props="{ 'data-testid': 'row' }" @update:sort="(v) => { sort = v; events.push('sort:' + v) }" @load-more="events.push('more')">
      <template #[\`item.name\`]="{ item }"><b class="nm">{{ item.name }}</b></template>
      <template #[\`item.prio\`]="{ item }">{{ item.prio }}</template>
    </DataTable>`,
  })
  const w = mount(Host, { global: { plugins: [createAppVuetify(), i18n] }, attachTo: document.body })
  const th = (key: string) => w.find(`thead th[data-col="${key}"]`)
  const names = () => w.findAll('[data-testid="row"] .nm').map((n) => n.text())
  return { w, cols, sort, events, th, names }
}

describe('DataTable', () => {
  beforeEach(() => {
    localStorage.clear()
    setActivePinia(createPinia())
  })

  const defs: ColumnDef[] = [
    { key: 'name', label: 'Name', locked: true, sort: { asc: 'name', desc: '-name' } },
    { key: 'prio', label: 'Priority', sort: { asc: 'prio', desc: '-prio' }, sortValue: (r: Row) => r.rank },
    { key: 'note', label: 'Note' },
  ]

  it('server sort: only columns with API sort values sort, a click emits the sort value, rows keep the server order', async () => {
    const h = harness({ serverSort: true, defs })
    expect(h.th('name').classes()).toContain('v-data-table__th--sortable')
    expect(h.th('note').classes()).not.toContain('v-data-table__th--sortable')

    await h.th('name').trigger('click')
    await nextTick()
    expect(h.events).toEqual(['sort:name'])
    expect(h.th('name').attributes('aria-sort')).toBe('ascending')
    // The server owns the order: the rows are not re-sorted in the browser.
    expect(h.names()).toEqual(['beta', 'Alpha', 'gamma'])

    await h.th('name').trigger('click')
    await h.th('name').trigger('click')
    expect(h.events).toEqual(['sort:name', 'sort:-name', 'sort:null'])
    h.w.unmount()
  })

  it('server sort: an external sort value shows on its column', async () => {
    const h = harness({ serverSort: true, defs })
    h.sort.value = '-prio'
    await nextTick()
    expect(h.th('prio').attributes('aria-sort')).toBe('descending')
    expect(h.th('name').attributes('aria-sort')).toBeUndefined()
    h.w.unmount()
  })

  it('client sort: sorts the loaded rows by the field or sortValue; sortValue false is not sortable', async () => {
    const h = harness({ defs: [{ key: 'name', label: 'Name' }, { key: 'prio', label: 'Priority', sortValue: (r: Row) => r.rank }, { key: 'note', label: 'Note', sortValue: false }] })
    expect(h.th('note').classes()).not.toContain('v-data-table__th--sortable')
    await h.th('name').trigger('click')
    expect(h.names()).toEqual(['Alpha', 'beta', 'gamma'])
    await h.th('prio').trigger('click')
    await h.th('prio').trigger('click')
    expect(h.names()).toEqual(['beta', 'gamma', 'Alpha'])
    expect(h.events).toEqual([])
    h.w.unmount()
  })

  it('cells carry their column key; a resizer on every declared header; hidden columns are not rendered', async () => {
    const h = harness({ serverSort: true, defs })
    expect(h.w.findAll('[data-testid="row"]').at(0)!.findAll('td').map((td) => td.attributes('data-col'))).toEqual(['name', 'prio', 'note'])
    expect(h.w.findAll('thead [data-resize-col]').map((e) => e.attributes('data-resize-col'))).toEqual(['name', 'prio', 'note'])
    h.cols.set('note', false)
    await nextTick()
    expect(h.th('note').exists()).toBe(false)
    h.w.unmount()
  })

  it('renders headers and cells in the user order', async () => {
    const h = harness({ serverSort: true, defs })
    h.cols.move('note', 0)
    await nextTick()
    expect(h.w.findAll('thead th').map((th) => th.attributes('data-col'))).toEqual(['note', 'name', 'prio'])
    expect(h.w.findAll('[data-testid="row"]').at(0)!.findAll('td').map((td) => td.attributes('data-col'))).toEqual(['note', 'name', 'prio'])
    h.w.unmount()
  })

  it('resizes with the arrow keys on the focused handle, without sorting', async () => {
    const h = harness({ serverSort: true, defs })
    await h.w.find('[data-resize-col="name"]').trigger('keydown', { key: 'ArrowRight' })
    expect(h.cols.sized.value).toBe(true)
    const before = h.cols.width('name')!
    await h.w.find('[data-resize-col="name"]').trigger('keydown', { key: 'ArrowLeft' })
    expect(h.cols.width('name')).toBe(Math.max(48, before - 16))
    await h.w.find('[data-resize-col="name"]').trigger('keydown', { key: 'Enter' })
    expect(h.events).toEqual([])
    h.w.unmount()
  })

  it('resized columns: fixed widths and a fixed table layout', async () => {
    const h = harness({ serverSort: true, defs })
    expect(h.w.find('.fd-dt').classes()).not.toContain('fd-dt--sized')
    h.cols.setWidths({ name: 200, prio: 90, note: 120 })
    await nextTick()
    expect(h.w.find('.fd-dt').classes()).toContain('fd-dt--sized')
    expect(h.w.find('.fd-dt').attributes('style')).toContain('--fd-dt-width: 410px')
    expect(h.th('prio').attributes('style')).toContain('width: 90px')
    h.w.unmount()
  })

  it('scrolling the body to its end asks for the next page, only while there is one; no button', async () => {
    const h = harness({ serverSort: true, defs, hasMore: true })
    expect(h.w.find('[data-testid="load-more"]').exists()).toBe(false)
    scrollTableToEnd(h.w.element)
    expect(h.events).toEqual(['more'])
    h.w.unmount()
    const done = harness({ serverSort: true, defs, hasMore: false })
    scrollTableToEnd(done.w.element)
    expect(done.events).toEqual([])
    done.w.unmount()
  })
})
