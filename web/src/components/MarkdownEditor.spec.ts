// SPDX-License-Identifier: AGPL-3.0-only

import { mountApp, flush, waitMarkdownEditor, fillMarkdownEditor } from '@/test/mount'
import MarkdownEditor from './MarkdownEditor.vue'
import TiptapSurface from './editor/TiptapSurface.vue'
import { renderMarkdown } from '@/utils/markdown'

const ROUND_TRIP = [
  '# Title\n\n#### Small heading\n\nText',
  '## Heading\n\nParagraph with **bold**, *italic*, ~~strike~~, `code`, and [a link](https://example.com).',
  '- one\n- two\n\n1. first\n2. second',
  '> quote line\n\n```\ncode block\n```',
  'Hello {{requester.name}} and @agent.name ref #1042',
  '| A | B |\n| --- | --- |\n| 1 | 2 |',
  '![logo](/branding/logo.png)',
  'Escaped \\_underscore\\_',
]

async function mountEditor(props: Record<string, unknown> = {}) {
  const { wrapper, ...rest } = mountApp(MarkdownEditor, { props: { modelValue: '', ...props } })
  await waitMarkdownEditor(wrapper)
  return { wrapper, ...rest }
}

describe('MarkdownEditor (Tiptap)', () => {
  it('emits Markdown from source mode and submit on Ctrl+Enter', async () => {
    const { wrapper } = await mountEditor({ required: true })
    await fillMarkdownEditor(wrapper, 'new text')
    expect(wrapper.emitted('update:modelValue')!.at(-1)).toEqual(['new text'])
    await wrapper.find('[data-testid="md-source"]').trigger('keydown', { key: 'Enter', ctrlKey: true })
    expect(wrapper.emitted('submit')).toHaveLength(1)
  })

  it('emits pasted and dropped files', async () => {
    const { wrapper } = await mountEditor()
    await fillMarkdownEditor(wrapper, 'x')
    const file = new File(['a'], 'a.txt', { type: 'text/plain' })
    await wrapper.find('[data-testid="md-source"]').trigger('paste', {
      clipboardData: { files: [file] },
    })
    expect(wrapper.emitted('files')![0]).toEqual([[file]])
    await wrapper.find('[data-testid="md-source"]').trigger('drop', {
      dataTransfer: { files: [file] },
    })
    expect(wrapper.emitted('files')!.length).toBeGreaterThan(1)
  })

  it('insert() replaces characters before the caret in source mode', async () => {
    const { wrapper } = await mountEditor({ modelValue: 'AB' })
    await fillMarkdownEditor(wrapper, 'AB')
    const ta = wrapper.find('[data-testid="md-source"]').element as HTMLTextAreaElement
    ta.setSelectionRange(1, 1)
    const vm = wrapper.findComponent(TiptapSurface).vm as { insert: (t: string, n?: number) => Promise<void> }
    await vm.insert('Z', 0)
    await flush()
    expect(wrapper.emitted('update:modelValue')!.at(-1)).toEqual(['AZB'])
  })

  it('textBeforeCaret() returns text in the current block', async () => {
    const { wrapper } = await mountEditor()
    await fillMarkdownEditor(wrapper, 'ping @Be')
    const ta = wrapper.find('[data-testid="md-source"]').element as HTMLTextAreaElement
    ta.setSelectionRange('ping @Be'.length, 'ping @Be'.length)
    const vm = wrapper.findComponent(TiptapSurface).vm as { textBeforeCaret: () => string }
    expect(vm.textBeforeCaret()).toBe('ping @Be')
  })

  it('required rule shows an error on validate', async () => {
    const { wrapper } = await mountEditor({ required: true })
    const input = wrapper.findComponent({ name: 'VInput' })
    const valid = await (input.vm as { validate: () => Promise<true | string[]> }).validate()
    expect(valid).not.toBe(true)
    expect(wrapper.text()).toMatch(/required/i)
  })

  it('toggles Markdown source mode', async () => {
    const { wrapper } = await mountEditor({ modelValue: '**x**' })
    expect(wrapper.find('[data-testid="md-source"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="md-source"]').isVisible()).toBe(false)
    await wrapper.find('[data-testid="md-markdown-toggle"]').trigger('click')
    await flush()
    expect(wrapper.find('[data-testid="md-source"]').isVisible()).toBe(true)
  })

  it('round-trips stored Markdown without losing text, links or structure', async () => {
    // What a reader sees: the rendered text and the kind of every block and link target.
    const shape = (md: string) => {
      const doc = new DOMParser().parseFromString(renderMarkdown(md), 'text/html').body
      const tags = [...doc.querySelectorAll('h1,h2,h3,h4,h5,h6,ul,ol,blockquote,pre,table,img,a,strong,em,del,code')]
      return {
        text: doc.textContent!.replace(/\s+/g, ' ').trim(),
        tags: tags.map((e) => e.tagName + (e.getAttribute('href') ?? e.getAttribute('src') ?? '')),
      }
    }
    for (const md of ROUND_TRIP) {
      const { wrapper } = await mountEditor({ modelValue: md })
      const out = wrapper.findComponent(TiptapSurface).vm.getMarkdown()
      expect(shape(out), `${JSON.stringify(md)} → ${JSON.stringify(out)}`).toEqual(shape(md))
    }
  })
})
