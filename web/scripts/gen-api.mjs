// Generates src/api/schema.d.ts from ../api/openapi.yaml (CON-004).
// Until the spec exists, writes a permissive stub so the app still type-checks.
import { existsSync, writeFileSync, readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { execFileSync } from 'node:child_process'

const spec = fileURLToPath(new URL('../../api/openapi.yaml', import.meta.url))
const out = fileURLToPath(new URL('../src/api/schema.d.ts', import.meta.url))
if (!existsSync(spec)) {
  writeFileSync(
    out,
    '// Stub: api/openapi.yaml not found yet. Run `pnpm gen:api` once it exists.\n' +
      '/* eslint-disable */\nexport type paths = Record<string, any>\nexport type components = { schemas: Record<string, any> }\n',
  )
  console.log('gen:api: spec missing, wrote stub')
  process.exit(0)
}
execFileSync('npx', ['openapi-typescript', spec, '-o', out, '--root-types', '--alphabetize'], { stdio: 'inherit' })
const body = readFileSync(out, 'utf8')
if (!body.startsWith('/* eslint-disable */')) writeFileSync(out, '/* eslint-disable */\n' + body)
