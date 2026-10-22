<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { mdiFolderOutline, mdiBookOpenPageVariantOutline } from '@mdi/js'
import { api, unwrap } from '@/api/client'
import { ApiProblem } from '@/api/problem'
import EmptyState from '@/components/EmptyState.vue'
import KbArticleList from '../components/KbArticleList.vue'
import { localized, useKbCategories, type KbArticleSummary, type KbLocale } from '../kb'

/** `/kb/c/:slug` (FE-051): sub-categories and the category's articles, cursor-paged. */
const route = useRoute()
const { locale, t } = useI18n()
const { categories, load } = useKbCategories(() => locale.value)
const slug = computed(() => String(route.params.slug ?? ''))
const articles = ref<KbArticleSummary[]>([])
const next = ref<string | null>(null)
const loading = ref(true)
const error = ref<string | null>(null)

const category = computed(() => (categories.value ?? []).find((c) => c.slug === slug.value) ?? null)
const children = computed(() => (categories.value ?? []).filter((c) => category.value && c.parent_id === category.value.id))
const parent = computed(() => (categories.value ?? []).find((c) => category.value && c.id === category.value.parent_id) ?? null)
const title = computed(() => (category.value ? localized(category.value.title, locale.value) || category.value.slug : slug.value))

async function fetchPage(cursor?: string | null) {
  return unwrap(
    api.GET('/kb/articles', { params: { query: { category: slug.value, locale: locale.value as KbLocale, cursor: cursor ?? undefined, limit: 25 } } }),
  )
}

async function refresh() {
  loading.value = true
  error.value = null
  try {
    const [, page] = await Promise.all([load().catch(() => []), fetchPage()])
    articles.value = page.items
    next.value = page.next_cursor
  } catch (e) {
    error.value = e instanceof ApiProblem && e.status === 404 ? t('kb.categoryNotFound') : e instanceof ApiProblem ? e.userMessage : t('errors.generic')
  } finally {
    loading.value = false
  }
}

async function more() {
  try {
    const page = await fetchPage(next.value)
    articles.value.push(...page.items)
    next.value = page.next_cursor
  } catch (e) {
    error.value = e instanceof ApiProblem ? e.userMessage : t('errors.generic')
  }
}

watch([slug, locale], refresh, { immediate: true })
</script>

<template>
  <v-container class="py-8" style="max-width: 960px">
    <v-breadcrumbs
      class="px-0"
      :items="[{ title: $t('nav.help'), to: '/kb' }, ...(parent ? [{ title: localized(parent.title, locale) || parent.slug, to: `/kb/c/${parent.slug}` }] : []), { title }]"
    />
    <h1 class="text-h4 mb-6" data-testid="kb-category-title">{{ title }}</h1>
    <v-row v-if="children.length" class="mb-4">
      <v-col v-for="c in children" :key="c.id" cols="12" sm="6">
        <v-card :to="`/kb/c/${c.slug}`" border flat :prepend-icon="mdiFolderOutline" :title="localized(c.title, locale) || c.slug" />
      </v-col>
    </v-row>
    <v-progress-linear v-if="loading" indeterminate color="primary" />
    <EmptyState v-else-if="error" :title="error" body="">
      <v-btn to="/kb" color="primary">{{ $t('kb.backHome') }}</v-btn>
    </EmptyState>
    <EmptyState v-else-if="!articles.length" :icon="mdiBookOpenPageVariantOutline" :title="$t('kb.noArticles')" body="" />
    <template v-else>
      <v-card border flat><KbArticleList :articles="articles" /></v-card>
      <div v-if="next" class="text-center mt-4"><v-btn variant="outlined" @click="more">{{ $t('kb.loadMore') }}</v-btn></div>
    </template>
  </v-container>
</template>
