<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { mdiFolderOutline, mdiBookOpenPageVariantOutline } from '@mdi/js'
import { api, unwrap } from '@/api/client'
import { ApiProblem } from '@/api/problem'
import EmptyState from '@/components/EmptyState.vue'
import KbSearchBox from '../components/KbSearchBox.vue'
import KbArticleList from '../components/KbArticleList.vue'
import { localized, useKbCategories, type KbArticleSummary, type KbLocale } from '../kb'

/** `/kb` (FE-051): top-level categories + search over `/kb/articles?q=` (≥ 2 characters). */
const route = useRoute()
const router = useRouter()
const { locale, t } = useI18n()
const { categories, load } = useKbCategories(() => locale.value)
const q = ref(typeof route.query.q === 'string' ? route.query.q : '')
const results = ref<KbArticleSummary[]>([])
const searching = ref(false)
const error = ref<string | null>(null)
const catError = ref<string | null>(null)

const top = computed(() => (categories.value ?? []).filter((c) => !c.parent_id))
const active = computed(() => q.value.trim().length >= 2)

async function loadCategories() {
  try {
    await load()
  } catch (e) {
    catError.value = e instanceof ApiProblem ? e.userMessage : t('errors.generic')
  }
}

let seq = 0
async function search() {
  const text = q.value.trim()
  void router.replace({ query: text ? { q: text } : {} })
  if (text.length < 2) {
    results.value = []
    return
  }
  const my = ++seq
  searching.value = true
  error.value = null
  try {
    const page = await unwrap(api.GET('/kb/articles', { params: { query: { q: text, locale: locale.value as KbLocale, limit: 20 } } }))
    if (my === seq) results.value = page.items
  } catch (e) {
    if (my === seq) error.value = e instanceof ApiProblem ? e.userMessage : t('errors.generic')
  } finally {
    if (my === seq) searching.value = false
  }
}

watch(q, search)
watch(locale, () => {
  void load(true)
  if (active.value) void search()
})
onMounted(() => {
  void loadCategories()
  if (active.value) void search()
})
</script>

<template>
  <v-container class="py-8" style="max-width: 960px">
    <h1 class="text-h4 mb-2">{{ $t('nav.help') }}</h1>
    <p class="text-body-1 text-medium-emphasis mb-6">{{ $t('kb.intro') }}</p>
    <KbSearchBox v-model="q" class="mb-6" />

    <section v-if="active" aria-live="polite" data-testid="kb-results">
      <h2 class="text-h6 mb-2">{{ $t('kb.resultsFor', { q: q.trim() }) }}</h2>
      <v-progress-linear v-if="searching" indeterminate color="primary" class="mb-2" />
      <v-alert v-if="error" type="error" variant="tonal">{{ error }}</v-alert>
      <EmptyState v-else-if="!searching && !results.length" :icon="mdiBookOpenPageVariantOutline" :title="$t('kb.noResults')" :body="$t('kb.noResultsHint')" />
      <v-card v-else border flat><KbArticleList :articles="results" /></v-card>
    </section>

    <section v-else data-testid="kb-categories">
      <v-alert v-if="catError" type="error" variant="tonal">{{ catError }}</v-alert>
      <v-progress-linear v-else-if="categories === null" indeterminate color="primary" />
      <EmptyState v-else-if="!top.length" :icon="mdiBookOpenPageVariantOutline" :title="$t('kb.empty')" body="" />
      <v-row v-else>
        <v-col v-for="c in top" :key="c.id" cols="12" sm="6" md="4">
          <v-card :to="`/kb/c/${c.slug}`" border flat height="100%" data-testid="kb-category-card">
            <v-card-item :prepend-icon="mdiFolderOutline">
              <v-card-title class="text-wrap">{{ localized(c.title, locale) || c.slug }}</v-card-title>
              <v-card-subtitle v-if="c.article_count !== undefined">{{ $t('kb.articleCount', { n: c.article_count }, c.article_count) }}</v-card-subtitle>
            </v-card-item>
          </v-card>
        </v-col>
      </v-row>
    </section>
  </v-container>
</template>
