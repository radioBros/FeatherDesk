// SPDX-License-Identifier: AGPL-3.0-only

// @vitest-environment node
import { ESLint } from 'eslint'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const cwd = fileURLToPath(new URL('..', import.meta.url))

describe('lint gates (FE-005, FE-007)', () => {
  it('the bad fixture fails with all three local gates', async () => {
    const eslint = new ESLint({ cwd, ignore: false })
    const file = fileURLToPath(new URL('./fixtures/BadForm.vue', import.meta.url))
    const [res] = await eslint.lintText(readFileSync(file, 'utf8'), { filePath: fileURLToPath(new URL('../src/components/BadForm.vue', import.meta.url)) })
    const ids = new Set(res!.messages.map((m) => m.ruleId))
    expect(ids).toContain('local/rules-from-validations')
    expect(ids).toContain('local/form-submit-validates')
    expect(ids).toContain('no-restricted-syntax')
  })
  it('the real source tree passes', async () => {
    const eslint = new ESLint({ cwd })
    const results = await eslint.lintFiles(['src/**/*.{ts,vue}'])
    const errors = results.flatMap((r) => r.messages.filter((m) => m.severity === 2).map((m) => `${r.filePath}:${m.line} ${m.ruleId}`))
    expect(errors).toEqual([])
  }, 60_000)
})
