// Fails when any locale is missing a key that another locale has (FE-003, FE-058).
// Generic over every file in src/locales, so adding a language needs no code change.
// Area message packs in sub-directories (e.g. src/locales/agent/<locale>.json,
// loaded lazily by that area) are checked the same way, each as its own set.
import { readdirSync, readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { join } from 'node:path'

const root = fileURLToPath(new URL('../src/locales', import.meta.url))
const flat = (obj, prefix = '') =>
  Object.entries(obj).flatMap(([k, v]) =>
    v && typeof v === 'object' ? flat(v, `${prefix}${k}.`) : [`${prefix}${k}`],
  )
const get = (o, path) => path.split('.').reduce((a, k) => (a == null ? a : a[k]), o)

function check(dir, label) {
  const files = readdirSync(dir).filter((f) => f.endsWith('.json'))
  const data = Object.fromEntries(files.map((f) => [f, JSON.parse(readFileSync(join(dir, f), 'utf8'))]))
  const keys = Object.fromEntries(files.map((f) => [f, new Set(flat(data[f]))]))
  const all = new Set(Object.values(keys).flatMap((s) => [...s]))
  let missing = 0
  for (const [file, set] of Object.entries(keys)) {
    for (const k of all) {
      if (!set.has(k)) {
        console.error(`i18n${label}: ${file} is missing "${k}"`)
        missing++
      }
    }
  }
  // Placeholders such as {n} must match the reference locale.
  const ref = data['en.json'] ?? {}
  for (const file of files) {
    for (const k of keys['en.json'] ?? []) {
      const a = String(get(ref, k) ?? '').match(/\{\w+\}/g)?.sort().join() ?? ''
      const b = String(get(data[file], k) ?? '').match(/\{\w+\}/g)?.sort().join() ?? ''
      if (a !== b) {
        console.error(`i18n${label}: ${file} "${k}" placeholders ${b || '∅'} differ from en ${a || '∅'}`)
        missing++
      }
      // vue-i18n message syntax: plural branches `|`, linked messages `@`, literals `{'…'}`.
      // A stray special character compiles to a different message, so the counts must match en.
      for (const ch of ['|', '@', '{', '}']) {
        const count = (s) => String(s ?? '').split(ch).length - 1
        if (count(get(ref, k)) !== count(get(data[file], k))) {
          console.error(`i18n${label}: ${file} "${k}" has ${count(get(data[file], k))} "${ch}", en has ${count(get(ref, k))}`)
          missing++
        }
      }
    }
  }
  if (missing) console.error(`i18n${label}: ${missing} problem(s) across ${files.length} locales`)
  else console.log(`i18n${label}: ${all.size} keys complete in ${files.length} locales (${files.join(', ')})`)
  return missing
}

let problems = check(root, '')
for (const sub of readdirSync(root, { withFileTypes: true }).filter((d) => d.isDirectory())) {
  problems += check(join(root, sub.name), `[${sub.name}]`)
}
if (problems) process.exit(1)
