// SPDX-License-Identifier: AGPL-3.0-only

import { negotiateLocale, setLocale, i18n, SUPPORTED_LOCALES } from './index'
import { defineComponent, h } from 'vue'
import { VDataTable } from 'vuetify/components'
import { mountApp, flush } from '@/test/mount'

const modules = import.meta.glob('../locales/*.json', { eager: true }) as Record<string, { default: Record<string, unknown> }>
const flat = (o: Record<string, unknown>, p = ''): string[] =>
  Object.entries(o).flatMap(([k, v]) => (v && typeof v === 'object' ? flat(v as Record<string, unknown>, `${p}${k}.`) : [`${p}${k}`]))

describe('i18n (FE-003)', () => {
  it('every locale file has exactly the keys of en.json (generic over the locale list)', () => {
    const files = Object.entries(modules)
    expect(files.length).toBe(SUPPORTED_LOCALES.length)
    const ref = new Set(flat(modules['../locales/en.json']!.default))
    for (const [name, mod] of files) {
      const keys = new Set(flat(mod.default))
      expect([...ref].filter((k) => !keys.has(k)), `${name} missing`).toEqual([])
      expect([...keys].filter((k) => !ref.has(k)), `${name} extra`).toEqual([])
    }
  })
  it('negotiates user pref → instance default → browser → en', () => {
    expect(negotiateLocale('it', 'de', ['fr-FR'])).toBe('it')
    expect(negotiateLocale(null, 'de', ['fr-FR'])).toBe('de')
    expect(negotiateLocale(undefined, undefined, ['xx', 'fr-FR'])).toBe('fr')
    expect(negotiateLocale('zz', 'yy', ['qq'])).toBe('en')
    expect(negotiateLocale('it', null, [], ['en', 'de'])).toBe('en')
  })
  it('loads a lazy locale and switches', async () => {
    await setLocale('de')
    expect(i18n.global.locale.value).toBe('de')
    expect(i18n.global.t('app.save')).toBe('Speichern')
    expect(document.documentElement.lang).toBe('de')
    await setLocale('en')
    expect(i18n.global.t('app.save')).toBe('Save')
  })
  it('translates Vuetify internal strings through the adapter (it, de)', async () => {
    await setLocale('it')
    const Table = defineComponent({ render: () => h(VDataTable, { items: [], headers: [{ title: 'X', key: 'x' }] }) })
    const { wrapper } = mountApp(Table)
    await flush()
    expect(wrapper.text()).toContain('Nessun elemento disponibile')
    wrapper.unmount()
    await setLocale('de')
    const de = mountApp(Table).wrapper
    await flush()
    expect(de.text()).toContain('Keine Daten vorhanden')
    await setLocale('en')
  })
})
