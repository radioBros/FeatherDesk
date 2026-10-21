// SPDX-License-Identifier: AGPL-3.0-only

import { defineConfig } from 'vitest/config'
import type { Plugin } from 'vite'
import { readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import vue from '@vitejs/plugin-vue'
import vuetify from 'vite-plugin-vuetify'
import { fileURLToPath, URL } from 'node:url'
import { gzipSync } from 'node:zlib'

// Writes a .gz next to every compressible asset so the Go server can serve
// pre-compressed files without compressing at request time (PLT-003). It runs
// after the bundle is on disk: compressing in generateBundle would capture code
// before Vite's own final replacements (e.g. the __VITE_PRELOAD__ marker).
function precompress(): Plugin {
  let outDir = 'dist'
  return {
    name: 'st-precompress',
    apply: 'build',
    enforce: 'post',
    configResolved(c) {
      outDir = c.build.outDir
    },
    writeBundle(_opts, bundle) {
      const root = fileURLToPath(new URL('./', import.meta.url))
      for (const name of Object.keys(bundle)) {
        if (!/\.(js|css|svg|json|html|txt)$/.test(name)) continue
        const file = join(root, outDir, name)
        const buf = readFileSync(file)
        if (buf.length < 1024) continue
        writeFileSync(file + '.gz', gzipSync(buf, { level: 9 }))
      }
    },
  }
}

// `@mdi/js` is one module, so every icon used anywhere would land in one shared
// chunk that every first view loads (FE-031 budget). In the build, each
// `import { mdiX } from '@mdi/js'` becomes an inline string constant, so an icon
// ships only inside the chunks that use it (a path is a few hundred bytes).
function mdiInline(): Plugin {
  let icons: Record<string, string> | undefined
  return {
    name: 'st-mdi-inline',
    apply: 'build',
    enforce: 'pre',
    async transform(code, id) {
      if (id.includes('/node_modules/') || id.includes('?') || !/\.(ts|vue)$/.test(id) || !code.includes('@mdi/js')) return
      icons ??= (await import('@mdi/js')) as unknown as Record<string, string>
      const all = icons
      return code.replace(/import\s*\{([^}]+)\}\s*from\s*['"]@mdi\/js['"]/g, (_m, names: string) =>
        names
          .split(',')
          .map((n) => n.trim())
          .filter(Boolean)
          .map((n) => {
            const [name, alias] = n.split(/\s+as\s+/) as [string, string?]
            const path = all[name]
            if (typeof path !== 'string') this.error(`@mdi/js has no icon ${name}`)
            return `const ${alias ?? name} = ${JSON.stringify(path)}`
          })
          .join('\n'),
      )
    },
  }
}

// web/dist/.gitkeep keeps the go:embed directory present in a fresh clone.
function keepDir(): Plugin {
  return {
    name: 'st-keep-dist',
    apply: 'build',
    closeBundle() {
      writeFileSync(fileURLToPath(new URL('./dist/.gitkeep', import.meta.url)), '')
    },
  }
}

/**
 * FE-031: modules every area's first view needs (Vue, the router/i18n/store
 * runtime and the Vuetify pieces of the login and layout shells) go into one
 * `core` chunk. Left to the default splitting they scatter over ~20 small
 * shared chunks, and the per-chunk overhead plus the lost cross-file gzip
 * context cost every first view several KB. The list is the intersection of
 * the four first-view graphs (`pnpm budget`); anything used only by a lazy
 * page stays out of it.
 */
const CORE_COMPOSABLES =
  'autocomplete|autofocus|border|color|component|date|defaults|delay|density|dimensions|display|elevation|focus|focusTrap|form|forwardRefs|goto|group|hydration|iconSizes|icons|layout|lazy|list-items|loader|locale|location|nested|position|proxiedModel|reveal|rounded|router|rules|scopeId|selectLink|size|ssrBoot|stack|tag|teleport|theme|toggleScope|transition|validation|variant'
const CORE_COMPONENTS =
  'VAlert|VApp|VAvatar|VBadge|VBtn|VBtnGroup|VBtnToggle|VCard|VCounter|VDefaultsProvider|VDialog|VDivider|VField|VGrid|VIcon|VImg|VInput|VLabel|VList|VMain|VMenu|VMessages|VOverlay|VProgressCircular|VProgressLinear|VResponsive|VSnackbar|VSnackbarQueue|VTextField|VThemeProvider|transitions'
const CORE = new RegExp(
  '[\\\\/]node_modules[\\\\/](?:.*[\\\\/])?(?:' +
    [
      '@vue[\\\\/]',
      'vue[\\\\/]dist[\\\\/]',
      '@vuetify[\\\\/]v0[\\\\/]',
      'vue-router[\\\\/]',
      'vue-i18n[\\\\/]',
      '@intlify[\\\\/]',
      'pinia[\\\\/]',
      'openapi-fetch[\\\\/]',
      'vuetify[\\\\/]lib[\\\\/](?:util[\\\\/]|framework\\.js|icons\\.js|iconsets[\\\\/]|locale[\\\\/](?:adapters[\\\\/]|en\\.js)|directives[\\\\/](?:ripple|intersect|click-outside|touch-hold)[\\\\/])',
      `vuetify[\\\\/]lib[\\\\/]composables[\\\\/](?:${CORE_COMPOSABLES})(?:\\.js$|[\\\\/])`,
      `vuetify[\\\\/]lib[\\\\/]components[\\\\/](?:${CORE_COMPONENTS})[\\\\/]`,
    ].join('|') +
    ')',
)
const isCoreModule = (id: string) => CORE.test(id)

const backend = process.env.ST_DEV_BACKEND ?? 'http://localhost:8080'

export default defineConfig({
  plugins: [mdiInline(), vue(), vuetify({ autoImport: { ignore: ['VBtn'] } }), precompress(), keepDir()],
  resolve: { alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) } },
  server: {
    port: 5173,
    proxy: {
      '/api': { target: backend, changeOrigin: false, ws: true },
      '/branding': { target: backend, changeOrigin: false },
      '/inbound': { target: backend, changeOrigin: false },
      '/kb/sitemap.xml': { target: backend, changeOrigin: false },
      '/healthz': { target: backend },
      '/readyz': { target: backend },
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    manifest: true,
    assetsDir: 'assets',
    target: 'es2022',
    sourcemap: false,
    chunkSizeWarningLimit: 600,
    rolldownOptions: { output: { codeSplitting: { groups: [{ name: 'core', test: isCoreModule }] } } },
  },
  test: {
    globals: true,
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.spec.ts', 'eslint-rules/**/*.spec.ts'],
    server: { deps: { inline: ['vuetify'] } },
    css: false,
  },
})
