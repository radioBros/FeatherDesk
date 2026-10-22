// SPDX-License-Identifier: AGPL-3.0-only

import { config } from '@vue/test-utils'

// jsdom gaps used by Vuetify overlays (skipped in `@vitest-environment node` specs).
const isDom = typeof window !== 'undefined'
if (isDom && !('ResizeObserver' in globalThis)) {
  ;(globalThis as any).ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
}
if (isDom && !('visualViewport' in window)) {
  ;(window as any).visualViewport = { width: 1024, height: 768, addEventListener() {}, removeEventListener() {} }
}

config.global.stubs = { transition: false, 'transition-group': false }
if (isDom && !window.matchMedia) {
  window.matchMedia = ((q: string) => ({
    matches: false,
    media: q,
    onchange: null,
    addListener() {},
    removeListener() {},
    addEventListener() {},
    removeEventListener() {},
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia
}
if (isDom && !('IntersectionObserver' in globalThis)) {
  ;(globalThis as any).IntersectionObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
    takeRecords() { return [] }
  }
}

// MarkdownView loads markdown-it + DOMPurify lazily (FE-031); specs start warm
// so rendered Markdown is in the DOM on the first tick, as after the first load.
if (isDom) await (await import('@/utils/markdownLoader')).loadMarkdown()
