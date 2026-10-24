// SPDX-License-Identifier: AGPL-3.0-only

import NewTicketForm from './forms/NewTicketForm.vue'
import MarkdownEditor from '@/components/MarkdownEditor.vue'
import { mockFetch, json, mountApp } from '@/test/mount'
import { requests } from './testing'

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

const SUGGESTION = { id: 'a1', slug: 'reset-printer', title: 'How to reset the printer', excerpt: 'Hold the button', locale: 'en', updated_at: '2026-10-20T10:00:00Z' }

describe('KB suggestions while typing the subject (FE-051)', () => {
  it('typing triggers one debounced request and opening an article keeps the draft', async () => {
    vi.useFakeTimers()
    const { fn } = mockFetch((url) =>
      url.includes('/portal/kb/suggest') ? json([SUGGESTION]) : json({ ...SUGGESTION, body_md: 'Hold the **reset** button for 5 s.' }),
    )
    const { wrapper } = mountApp(NewTicketForm, { props: {} })
    await vi.advanceTimersByTimeAsync(0)
    const subject = wrapper.find('[data-testid="nt-subject"] input')
    await subject.setValue('p')
    await subject.setValue('pr')
    await subject.setValue('pri')
    await subject.setValue('printer')
    await wrapper.findComponent(MarkdownEditor).setValue('My draft text')
    await vi.advanceTimersByTimeAsync(299)
    expect(fn).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(10)
    await vi.advanceTimersByTimeAsync(10)
    const reqs = await requests(fn)
    expect(reqs).toHaveLength(1)
    expect(reqs[0]!.path).toBe('/portal/kb/suggest')
    expect(new URLSearchParams(reqs[0]!.search).get('q')).toBe('printer')

    await vi.advanceTimersByTimeAsync(10)
    const item = wrapper.find('[data-testid="kb-suggestion"]')
    expect(item.text()).toContain('How to reset the printer')
    await item.trigger('click')
    for (let i = 0; i < 5; i++) await vi.advanceTimersByTimeAsync(50)
    expect(document.body.querySelector('[data-testid="kb-sheet-body"]')?.innerHTML).toContain('<strong>reset</strong>')
    expect((wrapper.find('[data-testid="nt-subject"] input').element as HTMLInputElement).value).toBe('printer')
    expect(wrapper.findComponent(MarkdownEditor).props('modelValue')).toBe('My draft text')
    wrapper.unmount()
  })

  it('fewer than 2 characters sends nothing', async () => {
    vi.useFakeTimers()
    const { fn } = mockFetch(() => json([]))
    const { wrapper } = mountApp(NewTicketForm, { props: {} })
    await wrapper.find('[data-testid="nt-subject"] input').setValue('p')
    await vi.advanceTimersByTimeAsync(1000)
    expect(fn).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
