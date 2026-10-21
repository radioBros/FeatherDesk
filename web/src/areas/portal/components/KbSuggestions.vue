<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useDisplay } from 'vuetify'
import { mdiBookOpenPageVariantOutline, mdiClose } from '@mdi/js'
import { api, unwrap } from '@/api/client'
import { useConfigStore } from '@/stores/config'
import MarkdownView from '@/components/MarkdownView.vue'
import type { KbArticleSummary } from '../types'

/**
 * Suggested KB articles while the subject is typed (FE-051): one debounced
 * request per pause (300 ms, ≥ 2 characters). Articles open in a side sheet so
 * the ticket draft stays untouched.
 */
const props = defineProps<{ query: string }>()
const { locale, t } = useI18n()
const config = useConfigStore()
const { smAndDown } = useDisplay()
const items = ref<KbArticleSummary[]>([])
const open = ref(false)
const article = ref<{ title: string; body_md?: string } | null>(null)
const articleError = ref<string | null>(null)
let timer: ReturnType<typeof setTimeout> | null = null
let seq = 0

watch(
  () => props.query,
  (q) => {
    if (timer) clearTimeout(timer)
    const text = q.trim()
    if (!config.config.kb_enabled || text.length < 2) {
      items.value = []
      return
    }
    timer = setTimeout(async () => {
      const my = ++seq
      try {
        const res = await unwrap(api.GET('/portal/kb/suggest', { params: { query: { q: text.slice(0, 200), locale: locale.value as never } } }))
        if (my === seq) items.value = (res ?? []).slice(0, 5)
      } catch {
        if (my === seq) items.value = []
      }
    }, 300)
  },
)
onBeforeUnmount(() => timer && clearTimeout(timer))

async function show(a: KbArticleSummary) {
  open.value = true
  article.value = { title: a.title }
  articleError.value = null
  try {
    const full = await unwrap(api.GET('/kb/articles/{slug}', { params: { path: { slug: a.slug }, query: { locale: locale.value as never } } }))
    article.value = { title: full.title, body_md: full.body_md }
  } catch {
    articleError.value = t('errors.generic')
  }
}
</script>

<template>
  <div v-if="items.length" class="mb-4" data-testid="kb-suggestions">
    <div class="text-body-2 text-medium-emphasis mb-1">{{ $t('portal.newTicket.suggestions') }}</div>
    <v-list density="compact" border rounded class="py-0">
      <v-list-item
        v-for="a in items"
        :key="a.id"
        :prepend-icon="mdiBookOpenPageVariantOutline"
        :title="a.title"
        :subtitle="a.excerpt"
        data-testid="kb-suggestion"
        @click="show(a)"
      />
    </v-list>
    <!-- A dialog sheet over the form: the draft below stays mounted and untouched. -->
    <v-dialog v-model="open" max-width="720" scrollable :fullscreen="smAndDown">
      <v-card data-testid="kb-sheet">
        <v-card-title class="d-flex align-center">
          <h2 class="text-h6 flex-grow-1 text-wrap">{{ article?.title }}</h2>
          <v-btn :icon="mdiClose" variant="text" :aria-label="$t('app.close')" @click="open = false" />
        </v-card-title>
        <v-divider />
        <v-card-text>
          <v-alert v-if="articleError" type="error" variant="tonal">{{ articleError }}</v-alert>
          <MarkdownView v-else-if="article?.body_md" :source="article.body_md" data-testid="kb-sheet-body" />
          <v-progress-linear v-else indeterminate />
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="tonal" color="primary" data-testid="kb-sheet-back" @click="open = false">{{ $t('portal.newTicket.backToTicket') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>
  </div>
</template>
