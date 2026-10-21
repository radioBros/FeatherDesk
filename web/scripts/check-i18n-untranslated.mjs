// FE-058: flags any de/fr/es value that is identical to the English one, so no
// English text ships in a translated locale. Exceptions live in
// scripts/i18n-allowlist.json:
//   "*"          brand and technical terms that stay as-is in every language
//                (product names, protocol and auth-mechanism names, font names…)
//   "<locale>"   words spelled exactly like the English one in that language
//                (e.g. German "Name", French "Description", Spanish "No"), each
//                checked by a translator, never a placeholder for a missing string.
// Values made only of placeholders, numbers, units and punctuation are ignored.
// Checks src/locales/<locale>.json and every area pack src/locales/<dir>/<locale>.json.
import { readdirSync, readFileSync, existsSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { join } from 'node:path'

const root = fileURLToPath(new URL('../src/locales', import.meta.url))
const allow = JSON.parse(readFileSync(fileURLToPath(new URL('./i18n-allowlist.json', import.meta.url)), 'utf8'))
const LOCALES = ['de', 'fr', 'es']

const flat = (obj, prefix = '') =>
  Object.entries(obj).flatMap(([k, v]) => (v && typeof v === 'object' ? flat(v, `${prefix}${k}.`) : [[`${prefix}${k}`, v]]))

// Nothing translatable left once placeholders, numbers and unit abbreviations are gone.
const UNITS = /\b(min|h|d|s|ms|KB|MB|GB|px|%)\b/g
const trivial = (s) => !s.replace(/\{\w+\}/g, '').replace(UNITS, '').replace(/[\d\s\p{P}\p{S}]/gu, '')

const globalAllow = new Set(allow['*'] ?? [])
const used = new Map(LOCALES.map((l) => [l, new Set()]))
used.set('*', new Set())
const problems = []

const dirs = ['', ...readdirSync(root, { withFileTypes: true }).filter((d) => d.isDirectory()).map((d) => d.name)]
for (const dir of dirs) {
  const base = join(root, dir)
  const en = Object.fromEntries(flat(JSON.parse(readFileSync(join(base, 'en.json'), 'utf8'))))
  for (const locale of LOCALES) {
    const file = join(base, `${locale}.json`)
    if (!existsSync(file)) continue
    const localeAllow = new Set(allow[locale] ?? [])
    const tr = Object.fromEntries(flat(JSON.parse(readFileSync(file, 'utf8'))))
    for (const [key, value] of Object.entries(en)) {
      if (typeof value !== 'string' || tr[key] !== value || trivial(value)) continue
      if (globalAllow.has(value)) {
        used.get('*').add(value)
        continue
      }
      if (localeAllow.has(value)) {
        used.get(locale).add(value)
        continue
      }
      problems.push(`${dir ? dir + '/' : ''}${locale}.json "${key}" is still English: ${JSON.stringify(value)}`)
    }
  }
}

// Stale allowlist entries hide nothing and only invite misuse: fail on them too.
for (const [scope, list] of Object.entries(allow)) {
  if (scope.startsWith('_')) continue
  for (const v of list) {
    if (!used.get(scope)?.has(v)) problems.push(`i18n-allowlist.json "${scope}" entry ${JSON.stringify(v)} is unused`)
  }
}

if (problems.length) {
  for (const p of problems) console.error(`i18n-untranslated: ${p}`)
  console.error(`i18n-untranslated: ${problems.length} problem(s)`)
  process.exit(1)
}
console.log(`i18n-untranslated: no English left in ${LOCALES.join('/')} (${dirs.length} message sets)`)
