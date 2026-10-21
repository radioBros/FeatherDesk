// SPDX-License-Identifier: AGPL-3.0-only

import { mountApp, flush, mockFetch, json } from '@/test/mount'
import MessageThread from './MessageThread.vue'
import MarkdownEditor from './MarkdownEditor.vue'
import MarkdownView from './MarkdownView.vue'
import AttachmentUploader from './AttachmentUploader.vue'
import AttachmentList from './AttachmentList.vue'
import { useConfigStore } from '@/stores/config'
import type { Message } from '@/types'

afterEach(() => vi.unstubAllGlobals())

const messages: Message[] = [
  { id: '1', kind: 'public', body_md: 'Hello from customer', author: { id: 'c', name: 'Cust', kind: 'customer' }, created_at: new Date().toISOString() },
  { id: '2', kind: 'internal', body_md: 'SECRET internal zebra', author: { id: 's', name: 'Agent', kind: 'staff' }, created_at: new Date().toISOString() },
  { id: '3', kind: 'system', body_md: 'Escalated to L2', author: null, created_at: new Date().toISOString() },
]

describe('MessageThread (FE-011)', () => {
  it('portal variant renders zero nodes of internal or system messages (I-2 second line)', () => {
    const { wrapper } = mountApp(MessageThread, { props: { messages, area: 'portal' } })
    expect(wrapper.html()).not.toContain('SECRET')
    expect(wrapper.html()).not.toContain('Escalated')
    expect(wrapper.findAll('[data-testid="msg-internal"]')).toHaveLength(0)
    expect(wrapper.text()).toContain('Hello from customer')
  })
  it('is an accessible log with labelled items', () => {
    const { wrapper } = mountApp(MessageThread, { props: { messages, area: 'agent' } })
    const log = wrapper.find('[role="log"]')
    expect(log.attributes('aria-label')).toBe('Conversation')
    const items = wrapper.findAll('[role="article"]')
    expect(items.length).toBe(3)
    for (const i of items) expect(i.attributes('aria-label')).toBeTruthy()
  })
  it('agent variant shows internal notes with a distinct label', () => {
    const { wrapper } = mountApp(MessageThread, { props: { messages, area: 'agent' } })
    const internal = wrapper.find('[data-testid="msg-internal"]')
    expect(internal.exists()).toBe(true)
    expect(internal.text()).toContain('Internal note')
    expect(wrapper.find('[data-testid="msg-system"]').exists()).toBe(true)
  })
  it('empty thread shows the empty state', () => {
    expect(mountApp(MessageThread, { props: { messages: [], area: 'agent' } }).wrapper.text()).toContain('No messages yet.')
  })
})

describe('Markdown components (FE-010)', () => {
  it('MarkdownView sanitises', () => {
    const { wrapper } = mountApp(MarkdownView, { props: { source: '**hi** <img src=x onerror=alert(1)>' } })
    expect(wrapper.html()).toContain('<strong>hi</strong>')
    expect(wrapper.find('img').exists()).toBe(false) // escaped as text, never an element
  })
  it('MarkdownEditor emits input, submit on Ctrl+Enter, and previews', async () => {
    const { wrapper } = mountApp(MarkdownEditor, { props: { modelValue: '**x**', required: true } })
    const ta = wrapper.find('textarea')
    await ta.setValue('new text')
    expect(wrapper.emitted('update:modelValue')!.at(-1)).toEqual(['new text'])
    await ta.trigger('keydown', { key: 'Enter', ctrlKey: true })
    expect(wrapper.emitted('submit')).toHaveLength(1)
    await wrapper.find('[data-testid="md-preview"]').trigger('click')
    await flush()
    expect(wrapper.find('[data-testid="md-preview-pane"]').html()).toContain('<strong>x</strong>')
  })
})

describe('Attachments (FE-012)', () => {
  it('rejects an oversize file client-side without any request', async () => {
    const { fn } = mockFetch(() => json({ id: 'a1' }))
    const { wrapper } = mountApp(AttachmentUploader, { props: { area: 'portal', modelValue: [] } })
    useConfigStore().config.upload = { max_bytes: 10, allowed_types: [], max_files: 5 }
    await (wrapper.vm as any).add([new File(['x'.repeat(100)], 'big.txt', { type: 'text/plain' })])
    await flush()
    expect(fn).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="upload-error"]').text()).toContain('larger than')
  })
  it('uploads and emits the draft attachment id', async () => {
    const { calls } = mockFetch(() => json({ id: 'att-1' }))
    const { wrapper } = mountApp(AttachmentUploader, { props: { area: 'agent', modelValue: [] } })
    await (wrapper.vm as any).add([new File(['hello'], 'a.txt', { type: 'text/plain' })])
    await flush()
    expect(calls[0]!.url).toBe('/api/v1/agent/attachments')
    expect(calls[0]!.init.method).toBe('POST')
    expect(wrapper.emitted('update:modelValue')!.at(-1)).toEqual([['att-1']])
  })
  it('rejects disallowed types when the server restricts them', async () => {
    const { fn } = mockFetch(() => json({ id: 'x' }))
    const { wrapper } = mountApp(AttachmentUploader, { props: { area: 'portal', modelValue: [] } })
    useConfigStore().config.upload = { max_bytes: 1e6, allowed_types: ['image/*'], max_files: 5 }
    await (wrapper.vm as any).add([new File(['x'], 'a.exe', { type: 'application/x-msdownload' })])
    expect(fn).not.toHaveBeenCalled()
  })
  it('AttachmentList links to the area download endpoint', () => {
    const { wrapper } = mountApp(AttachmentList, {
      props: { area: 'portal', attachments: [{ id: 'a/1', filename: 'f.pdf', content_type: 'application/pdf', size_bytes: 2048 }] },
    })
    expect(wrapper.find('a').attributes('href')).toBe('/api/v1/portal/attachments/a%2F1')
    expect(wrapper.text()).toContain('2 KB')
  })
})
