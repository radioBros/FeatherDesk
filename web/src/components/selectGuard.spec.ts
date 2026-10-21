// SPDX-License-Identifier: AGPL-3.0-only

import { readdirSync, readFileSync } from 'node:fs'
import { join, relative } from 'node:path'

// Every select dropdown is `AutocompleteSearch` (search box, compact multi mode, async mode).
// A raw `<v-select` / `<v-autocomplete` may only appear inside AutocompleteSearch.vue itself.
// (`<v-combobox>` stays: it is free-text entry.)
const SRC = join(process.cwd(), 'src')

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, e.name)
    if (e.isDirectory()) walk(p, out)
    else if (/\.(vue|ts)$/.test(e.name) && !/\.spec\.ts$/.test(e.name)) out.push(p)
  }
  return out
}

describe('select guard', () => {
  it('no raw <v-select> or <v-autocomplete> outside AutocompleteSearch.vue', () => {
    const offenders = walk(SRC)
      .filter((f) => !f.endsWith(join('components', 'AutocompleteSearch.vue')))
      .filter((f) => /<v-(select|autocomplete)\b/i.test(readFileSync(f, 'utf8')))
      .map((f) => relative(SRC, f))
    expect(offenders, 'use <AutocompleteSearch> (@/components/AutocompleteSearch.vue)').toEqual([])
  })
})
