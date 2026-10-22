// SPDX-License-Identifier: AGPL-3.0-only

import MarkdownIt from 'markdown-it'
import DOMPurify from 'dompurify'

// Raw HTML is disabled at the parser and the output is sanitised again (FE-010).
const md = new MarkdownIt({ html: false, linkify: true, breaks: true, typographer: false })

let hooked = false
function ensureHook() {
  if (hooked) return
  hooked = true
  DOMPurify.addHook('afterSanitizeAttributes', (node) => {
    if (node.tagName === 'A') {
      const href = node.getAttribute('href') ?? ''
      if (!/^(https?:|mailto:|\/|#)/i.test(href)) node.removeAttribute('href')
      node.setAttribute('target', '_blank')
      node.setAttribute('rel', 'noopener noreferrer nofollow')
    }
    if (node.tagName === 'IMG') {
      const src = node.getAttribute('src') ?? ''
      if (!/^(https?:|\/)/i.test(src)) node.removeAttribute('src')
      node.setAttribute('loading', 'lazy')
    }
  })
}

const ALLOWED_TAGS = [
  'a', 'b', 'blockquote', 'br', 'code', 'del', 'em', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'hr', 'i', 'img',
  'li', 'ol', 'p', 'pre', 's', 'strong', 'table', 'tbody', 'td', 'th', 'thead', 'tr', 'ul',
]

export function renderMarkdown(src: string | null | undefined): string {
  ensureHook()
  const html = md.render(src ?? '')
  return DOMPurify.sanitize(html, {
    ALLOWED_TAGS,
    ALLOWED_ATTR: ['href', 'title', 'src', 'alt', 'target', 'rel', 'loading', 'colspan', 'rowspan', 'align'],
    ALLOW_DATA_ATTR: false,
  })
}
