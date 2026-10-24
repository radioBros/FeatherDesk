// SPDX-License-Identifier: AGPL-3.0-only

import StarterKit from '@tiptap/starter-kit'
import { Markdown } from '@tiptap/markdown'
import Placeholder from '@tiptap/extension-placeholder'
import Image from '@tiptap/extension-image'
import { Table, TableRow, TableHeader, TableCell } from '@tiptap/extension-table'
import type { Extensions } from '@tiptap/vue-3'

/** Inline images already present in stored Markdown (no upload UI in the editor). */
export const allowedImageSrc = (src: string): boolean =>
  src.startsWith('/branding/') || src.startsWith('/api/') || src.includes('/attachments')

export const buildEditorExtensions = (placeholder: string): Extensions => [
  StarterKit.configure({
    // Every level parses, so stored Markdown (KB articles use # and ####) survives an edit; the toolbar offers H2/H3.
    heading: { levels: [1, 2, 3, 4, 5, 6] },
    // Markdown has no underline: Ctrl+U would format text that is lost on save.
    underline: false,
    link: {
      openOnClick: false,
      autolink: true,
      protocols: ['http', 'https', 'mailto'],
      defaultProtocol: 'https',
    },
  }),
  Markdown,
  Placeholder.configure({ placeholder }),
  Image.configure({
    inline: true,
    allowBase64: false,
  }).extend({
    addAttributes() {
      return {
        ...this.parent?.(),
        src: {
          default: null,
          parseHTML: (el) => {
            const src = (el as HTMLImageElement).getAttribute('src')
            return src && allowedImageSrc(src) ? src : null
          },
        },
      }
    },
  }),
  Table.configure({ resizable: false }),
  TableRow,
  TableHeader,
  TableCell,
]
