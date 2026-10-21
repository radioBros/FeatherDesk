// Bundle budget (FE-031, CONVENTIONS §10): initial JS per area, gzip.
// Target 200 KB (warn) · hard limit 300 KB (fail). Also fails if the portal or
// kb graph statically pulls in any chunk from src/areas/agent/.
import { readFileSync, existsSync } from 'node:fs'
import { gzipSync } from 'node:zlib'
import { fileURLToPath } from 'node:url'
import { join } from 'node:path'

const dist = fileURLToPath(new URL('../dist', import.meta.url))
const manifestPath = join(dist, '.vite', 'manifest.json')
if (!existsSync(manifestPath)) {
  console.error('budget: dist/.vite/manifest.json missing — run `pnpm build` first')
  process.exit(1)
}
const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'))
const TARGET = 200 * 1024
const HARD = 300 * 1024

/** Static import closure of a manifest key (dynamic imports are lazy, so excluded). */
function closure(keys) {
  const seen = new Set()
  const walk = (k) => {
    if (seen.has(k) || !manifest[k]) return
    seen.add(k)
    for (const i of manifest[k].imports ?? []) walk(i)
  }
  keys.forEach(walk)
  return seen
}
const gz = (file) => gzipSync(readFileSync(join(dist, file)), { level: 9 }).length
const find = (src) => {
  const name = src.split('/').pop().replace(/\.\w+$/, '')
  // Shared chunks have no `src`; fall back to the chunk name.
  const key =
    Object.keys(manifest).find((k) => k === src || manifest[k].src === src) ??
    Object.keys(manifest).find((k) => manifest[k].name === name && k.endsWith('.js'))
  if (!key) throw new Error(`budget: ${src} not in manifest`)
  return key
}

// What a first visit to each area loads before the page is interactive.
const areas = {
  portal: ['index.html', 'src/areas/portal/PortalLayout.vue', 'src/areas/portal/PortalLoginPage.vue'],
  agent: ['index.html', 'src/areas/agent/AgentLayout.vue', 'src/areas/agent/AgentLoginPage.vue'],
  kb: ['index.html', 'src/areas/kb/KbLayout.vue', 'src/areas/kb/pages/KbHomePage.vue'],
  setup: ['index.html', 'src/areas/setup/SetupPage.vue'],
}

let failed = false
const rows = []
for (const [area, srcs] of Object.entries(areas)) {
  const keys = closure(srcs.map(find))
  const files = [...keys].map((k) => manifest[k].file).filter((f) => f.endsWith('.js'))
  const bytes = files.reduce((n, f) => n + gz(f), 0)
  const status = bytes > HARD ? 'FAIL' : bytes > TARGET ? 'over target' : 'ok'
  if (bytes > HARD) failed = true
  rows.push({ area, kb: +(bytes / 1024).toFixed(1), files: files.length, status })
  if (area === 'portal' || area === 'kb') {
    const leaked = [...keys].filter((k) => (manifest[k].src ?? '').startsWith('src/areas/agent/'))
    if (leaked.length) {
      failed = true
      console.error(`budget: ${area} statically imports agent chunks: ${leaked.join(', ')}`)
    }
  }
}
console.table(rows)
console.log(`budget: target ${TARGET / 1024} KB · hard limit ${HARD / 1024} KB (gzip, initial JS per area)`)
if (failed) {
  console.error('budget: FAILED')
  process.exit(1)
}
