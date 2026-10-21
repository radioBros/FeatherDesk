// SPDX-License-Identifier: AGPL-3.0-only

/**
 * FE-005 enforcement (CONVENTIONS §12):
 * 1. every component containing <v-form> is listed in the registry;
 * 2. each registered form, submitted empty, sends no request (validate() ran first).
 */
import { formRegistry } from './registry'
import { mountApp, flush, mockFetch, json } from '@/test/mount'

const sources = import.meta.glob('/src/**/*.vue', { query: '?raw', import: 'default', eager: true }) as Record<string, string>

afterEach(() => vi.unstubAllGlobals())

describe('form registry (FE-005)', () => {
  it('lists every component that renders <v-form>', () => {
    const withForm = Object.entries(sources)
      .filter(([, src]) => /<v-form[\s>]/.test(src))
      .map(([path]) => path.replace(/^\/src\//, ''))
    const registered = new Set(formRegistry.map((f) => f.file))
    expect(withForm.filter((f) => !registered.has(f))).toEqual([])
    expect(withForm.length).toBeGreaterThan(0)
  })

  it.each(formRegistry.map((f) => [f.file, f] as const))('%s: empty submit sends no request', async (_file, entry) => {
    const { fn } = mockFetch(() => json({}))
    const mod = await entry.load()
    const { wrapper } = mountApp(mod.default, { props: entry.props ?? {} })
    await flush()
    await wrapper.find('form').trigger('submit')
    await flush()
    await flush()
    expect(fn).not.toHaveBeenCalled()
    // and the required-field messages are visible
    if (entry.emptyMessage !== false) expect(wrapper.text()).toContain(entry.emptyMessage ?? 'This field is required')
  })
})
