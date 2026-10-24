// SPDX-License-Identifier: AGPL-3.0-only

import { mountApp, flush } from '@/test/mount'
import MarkdownEditor from './MarkdownEditor.vue'

// The rich editor's chunk cannot be fetched (an old tab after an upgrade, a proxy that blocks it).
vi.mock('./editor/TiptapSurface.vue', () => {
  throw new Error('Failed to fetch dynamically imported module')
})

describe('MarkdownEditor without its rich editor chunk', () => {
  it('falls back to a plain Markdown text box that still edits and submits', async () => {
    const { wrapper } = mountApp(MarkdownEditor, { props: { modelValue: '', label: 'Reply' } })
    let box = wrapper.find('[data-testid="md-source"]')
    for (let i = 0; i < 50 && !box.exists(); i++) {
      await flush()
      box = wrapper.find('[data-testid="md-source"]')
    }
    expect(box.exists()).toBe(true)
    expect(box.attributes('aria-label')).toBe('Reply')
    await box.setValue('Plain **text** still works')
    expect(wrapper.emitted('update:modelValue')!.at(-1)).toEqual(['Plain **text** still works'])
    await box.trigger('keydown', { key: 'Enter', ctrlKey: true })
    expect(wrapper.emitted('submit')).toHaveLength(1)
  })
})
