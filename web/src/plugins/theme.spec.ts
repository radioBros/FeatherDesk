// SPDX-License-Identifier: AGPL-3.0-only

import { mountApp, flush } from '@/test/mount'
import { createAppVuetify } from './vuetify'
import { storedThemeMode, storeThemeMode, applyBranding } from './branding'
import BrandLogo from '@/components/BrandLogo.vue'
import { useConfigStore, DEFAULT_BRANDING } from '@/stores/config'
import { useTheme } from 'vuetify'
import { defineComponent, h } from 'vue'

describe('theme + branding at runtime (FE-002)', () => {
  it('the brand colour is in the theme before the first render', () => {
    const v = createAppVuetify({ ...DEFAULT_BRANDING, primary_color: '#aa00aa', default_theme: 'light' })
    expect(v.theme.themes.value.light!.colors.primary).toBe('#aa00aa')
  })
  it('the dark logo is used when the dark theme is active', async () => {
    const Probe = defineComponent({
      setup() {
        const theme = useTheme()
        return () => h('div', [h('button', { id: 'dark', onClick: () => theme.change('dark') }), h(BrandLogo)])
      },
    })
    const { wrapper } = mountApp(Probe)
    useConfigStore().setBranding({ logo_light_url: '/branding/light.png', logo_dark_url: '/branding/dark.png' })
    await flush()
    expect(wrapper.find('img').attributes('src')).toBe('/branding/light.png')
    await wrapper.find('#dark').trigger('click')
    await flush()
    expect(wrapper.find('img').attributes('src')).toBe('/branding/dark.png')
  })
  it('a live branding change re-applies a new colour without reload', () => {
    const v = createAppVuetify({ ...DEFAULT_BRANDING, primary_color: '#111111' })
    applyBranding(v, { ...DEFAULT_BRANDING, primary_color: '#22aa22' })
    expect(v.theme.themes.value.light!.colors.primary).toBe('#22aa22')
  })
  it('a throwing localStorage never breaks boot', () => {
    const spy = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('denied')
    })
    const spy2 = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('denied')
    })
    expect(storedThemeMode()).toBeNull()
    expect(() => storeThemeMode('dark')).not.toThrow()
    spy.mockRestore()
    spy2.mockRestore()
  })
})
