// SPDX-License-Identifier: AGPL-3.0-only

import { nextTick } from 'vue'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createAppVuetify } from '@/plugins/vuetify'
import { i18n } from '@/i18n'
import { useTableColumns, type ColumnDef } from '@/composables/useTableColumns'
import ColumnsMenu from './ColumnsMenu.vue'

const defs: ColumnDef[] = [
  { key: 'name', label: 'Name', locked: true },
  { key: 'email', label: 'Email' },
  { key: 'status', label: 'Status' },
]

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
