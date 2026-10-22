<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { mdiThumbUpOutline, mdiThumbDownOutline, mdiTranslate } from '@mdi/js'
import { api, unwrap } from '@/api/client'
import { ApiProblem } from '@/api/problem'
import { useSessionStore } from '@/stores/session'
import { useConfigStore } from '@/stores/config'
import EmptyState from '@/components/EmptyState.vue'
import MarkdownView from '@/components/MarkdownView.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import { feedback, localized, useKbCategories, type KbArticle, type KbLocale } from '../kb'

/**
 * `/kb/a/:slug` (FE-051): the article in the reader's locale; when the API falls
 * back to another locale a notice says so. "Was this helpful?" is sent once.
 */
const route = useRoute()
const router = useRouter()
const { locale, t } = useI18n()
const session = useSessionStore()
const config = useConfigStore()
const { categories, load } = useKbCategories(() => locale.value)
const slug = computed(() => String(route.params.slug ?? ''))
const article = ref<KbArticle | null>(null)
const loading = ref(true)
const error = ref<string | null>(null)
const voted = ref<boolean | undefined>(undefined)

const fallback = computed(() => !!article.value && article.value.locale !== locale.value)
const category = computed(() => (categories.value ?? []).find((c) => c.id === article.value?.category_id) ?? null)
const LOCALE_NAMES: Record<string, string> = { en: 'English', it: 'Italiano', de: 'Deutsch', fr: 'Français', es: 'Español' }

async function fetchArticle() {
  loading.value = true
  error.value = null
  try {
    article.value = await unwrap(api.GET('/kb/articles/{slug}', { params: { path: { slug: slug.value }, query: { locale: locale.value as KbLocale } } }))
    voted.value = feedback.get(article.value.id)
    // Old slugs answer with the article under its new slug: keep the URL canonical.
    if (article.value.slug && article.value.slug !== slug.value) void router.replace(`/kb/a/${article.value.slug}`)
  } catch (e) {
    article.value = null
    error.value = e instanceof ApiProblem && e.status === 404 ? t('kb.articleNotFound') : e instanceof ApiProblem ? e.userMessage : t('errors.generic')
  } finally {
    loading.value = false
  }
  void load().catch(() => null)
}

async function vote(helpful: boolean) {
  if (!article.value || feedback.has(article.value.id)) return
  voted.value = helpful
  await feedback.send(article.value.id, helpful)
}

watch([slug, locale], fetchArticle, { immediate: true })
</script>

<template>
  <v-container class="py-8" style="max-width: 860px">
    <v-progress-linear v-if="loading" indeterminate color="primary" />
    <EmptyState v-else-if="error || !article" :title="error ?? $t('kb.articleNotFound')" body="" data-testid="kb-article-error">
      <v-btn to="/kb" color="primary">{{ $t('kb.backHome') }}</v-btn>
    </EmptyState>
    <article v-else data-testid="kb-article">
      <v-breadcrumbs
        class="px-0"
        :items="[{ title: $t('nav.help'), to: '/kb' }, ...(category ? [{ title: localized(category.title, locale) || category.slug, to: `/kb/c/${category.slug}` }] : [])]"
      />
      <h1 class="text-h4 mb-2" data-testid="kb-article-title">{{ article.title }}</h1>
      <div class="text-body-2 text-medium-emphasis mb-4">{{ $t('kb.updated') }} <RelativeTime :value="article.updated_at" /></div>
      <v-alert v-if="fallback" type="info" variant="tonal" density="compact" :icon="mdiTranslate" class="mb-4" data-testid="kb-fallback">
        {{ $t('kb.fallbackNotice', { language: LOCALE_NAMES[article.locale] ?? article.locale }) }}
      </v-alert>
      <MarkdownView :source="article.body_md ?? ''" class="mb-8" data-testid="kb-article-body" />

      <v-divider class="mb-4" />
      <div class="d-flex flex-wrap align-center ga-3" data-testid="kb-feedback">
        <template v-if="voted === undefined">
          <span class="text-body-1">{{ $t('kb.helpful') }}</span>
          <v-btn variant="outlined" :prepend-icon="mdiThumbUpOutline" data-testid="kb-helpful-yes" @click="vote(true)">{{ $t('kb.yes') }}</v-btn>
          <v-btn variant="outlined" :prepend-icon="mdiThumbDownOutline" data-testid="kb-helpful-no" @click="vote(false)">{{ $t('kb.no') }}</v-btn>
        </template>
        <span v-else class="text-body-1" role="status" data-testid="kb-feedback-thanks">{{ $t('kb.thanks') }}</span>
      </div>
      <v-alert v-if="voted === false && config.config.portal_enabled !== false" type="info" variant="tonal" class="mt-4">
        {{ $t('kb.stillNeedHelp') }}
        <template #append>
          <v-btn :to="session.me ? '/tickets/new' : '/login?redirect=/tickets/new'" color="primary" variant="flat">{{ $t('nav.newTicket') }}</v-btn>
        </template>
      </v-alert>
    </article>
  </v-container>
</template>
