// SPDX-License-Identifier: AGPL-3.0-only

import { renderMarkdown } from './markdown'

describe('markdown rendering (FE-010)', () => {
  const corpus = [
    '<img src=x onerror=alert(1)>',
    '[click](javascript:alert(1))',
    '<svg onload=alert(1)>',
    '<script>alert(1)</script>',
    '<a href="javascript:alert(1)">x</a>',
    '![x](javascript:alert(1))',
    '<iframe src="https://evil"></iframe>',
    '[x](data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==)',
  ]
  it.each(corpus)('renders %s inert', (src) => {
    const html = renderMarkdown(src)
    const div = document.createElement('div')
    div.innerHTML = html
    expect(div.querySelector('script, iframe, svg')).toBeNull()
    for (const el of div.querySelectorAll('*')) {
      for (const attr of el.attributes) {
        expect(attr.name.startsWith('on')).toBe(false)
        expect(/^\s*(javascript|data):/i.test(attr.value)).toBe(false)
      }
    }
  })
  it('renders markdown and hardens links', () => {
    const html = renderMarkdown('**bold** and https://example.com')
    expect(html).toContain('<strong>bold</strong>')
    expect(html).toContain('rel="noopener noreferrer nofollow"')
    expect(html).toContain('target="_blank"')
  })
  it('handles empty input', () => {
    expect(renderMarkdown(null)).toBe('')
  })
})
