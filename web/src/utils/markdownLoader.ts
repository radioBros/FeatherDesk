// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Lazy access to the Markdown renderer (markdown-it + DOMPurify, FE-010): one
 * shared dynamic import, so the libraries load once, on the first render, and
 * stay out of every page chunk (FE-031).
 */
export type RenderMarkdown = (src: string | null | undefined) => string

let loaded: RenderMarkdown | null = null
let pending: Promise<RenderMarkdown> | null = null

/** The renderer if it is already loaded (lets a later view render synchronously). */
export function markdownRenderer(): RenderMarkdown | null {
  return loaded
}

export function loadMarkdown(): Promise<RenderMarkdown> {
  pending ??= import('./markdown').then(
    (m) => (loaded = m.renderMarkdown),
    (e) => {
      pending = null
      throw e
    },
  )
  return pending
}
