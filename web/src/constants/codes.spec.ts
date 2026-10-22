// SPDX-License-Identifier: AGPL-3.0-only

// @vitest-environment node
/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { errorCodes, ruleCodes } from './validations'

/**
 * FE-004: client messages must cover the server's field error codes. `FieldError.code`
 * references the `FieldErrorCode` enum; `errorCodes` is exactly that enum (client-only
 * rule codes live in `ruleCodes`), so every code the API can send has its own message.
 */
describe('field error codes vs OpenAPI', () => {
  const spec = readFileSync(fileURLToPath(new URL('../../../api/openapi.yaml', import.meta.url)), 'utf8')
  const enumCodes = (): string[] => {
    const at = spec.indexOf('    FieldErrorCode:')
    expect(at).toBeGreaterThan(0)
    const rest = spec.slice(at + '    FieldErrorCode:'.length)
    const next = rest.search(/\n {4}[A-Za-z]/) // the next schema at the same indentation
    const block = next > 0 ? rest.slice(0, next) : rest
    return [...block.split('enum:')[1]!.matchAll(/^\s+- "?([a-z_]+)"?$/gm)].map((m) => m[1]!)
  }
  it('FieldError.code is the FieldErrorCode enum', () => {
    const start = spec.indexOf('    FieldError:')
    const fe = spec.slice(start, spec.indexOf('\n    Problem:', start))
    expect(fe).toMatch(/code:\s+\$ref: "#\/components\/schemas\/FieldErrorCode"/)
    expect(enumCodes().length).toBeGreaterThan(40)
  })
  it('every server code has a client message, and errorCodes holds nothing else', () => {
    expect(new Set(Object.keys(errorCodes))).toEqual(new Set(enumCodes()))
  })
  it('client-only rule codes never shadow a server code', () => {
    for (const c of Object.keys(ruleCodes)) expect(enumCodes()).not.toContain(c)
  })
})
