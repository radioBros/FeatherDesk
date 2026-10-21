<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * /agent/kb/articles/new and /agent/kb/articles/:id (FE-052). Publish,
 * Unpublish, Archive and Delete exist only when the article's
 * `allowed_actions` lists them, and the form is read-only without `edit`:
 * the server decides by role and status (permissions §3, KB-013).
 */
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { mdiArchiveOutline, mdiArrowLeft, mdiDeleteOutline, mdiPublish, mdiPublishOff } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import KbArticleForm from './KbArticleForm.vue'
import { api, unwrap } from '@/api/client'
import { ApiProblem } from '@/api/problem'
import { useConfigStore } from '@/stores/config'
import { useUiStore } from '@/stores/ui'
import { useConfirm } from '@/composables/useConfirm'
import { useAction } from '../shared/useAction'
import { canKb, editorLocales, loadKbCategories, type KbArticleWithActions, type KbCategory } from './kb'
import type { components } from '@/api/schema'

type S = components['schemas']
type Text = { title: string; body_md: string }

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const config = useConfigStore()
const ui = useUiStore()
const { confirm } = useConfirm()
const { run, busy } = useAction()

const id = computed(() => (typeof route.params.id === 'string' ? route.params.id : null))
const article = ref<KbArticleWithActions | null>(null)
const translations = ref<Record<string, Text>>({})
const categories = ref<KbCategory[]>([])
const loading = ref(true)
const failed = ref<string | null>(null)
const formKey = ref(0)
const locales = computed(() => editorLocales(config.config))

const STATUS_COLOR: Record<S['KbStatus'], string> = { draft: 'secondary', published: 'success', archived: 'grey' }

async function loadArticle(articleId: string) {
  const a = await unwrap(api.GET('/agent/kb/articles/{id}', { params: { path: { id: articleId } } })) as KbArticleWithActions
  const map: Record<string, Text> = {}
  if (a.translations?.length) {
    for (const tr of a.translations) map[tr.locale] = { title: tr.title, body_md: tr.body_md }
  } else {
    // Older responses carry only the requested locale: fetch the others.
    map[a.locale] = { title: a.title, body_md: a.body_md ?? '' }
    const others = (a.available_locales ?? []).filter((l) => l !== a.locale)
    await Promise.all(
      others.map(async (l) => {
        try {
          const tr = await unwrap(api.GET('/agent/kb/articles/{id}/translations/{locale}', { params: { path: { id: articleId, locale: l } } }))
          map[l] = { title: tr.title, body_md: tr.body_md }
        } catch {
          /* a missing translation stays empty */
        }
      }),
    )
  }
  article.value = a
  translations.value = map
  formKey.value++
}

async function load() {
  loading.value = true
  failed.value = null
  try {
    const [cats] = await Promise.all([loadKbCategories(), id.value ? loadArticle(id.value) : Promise.resolve()])
    categories.value = cats
    if (!id.value) {
      article.value = null
      translations.value = {}
      formKey.value++
    }
  } catch (e) {
    failed.value = e instanceof ApiProblem ? e.userMessage : t('agent.common.loadFailed')
  } finally {
    loading.value = false
  }
}
onMounted(load)
watch(id, () => void load())

function onSaved(a: KbArticleWithActions) {
  ui.notify(t('agent.common.saved'), 'success')
  if (!id.value) void router.replace(`/agent/kb/articles/${a.id}`)
  else void loadArticle(a.id)
}

const STATUS_DONE: Record<S['KbStatus'], string> = { published: 'agent.kb.published', archived: 'agent.kb.archived', draft: 'agent.kb.unpublished' }

async function setStatus(status: S['KbStatus']) {
  if (!article.value) return
  const a = article.value
  const res = await run(
    () => unwrap(api.PATCH('/agent/kb/articles/{id}', { params: { path: { id: a.id } }, body: { status } })),
    t(STATUS_DONE[status]),
  )
  if (res) await loadArticle(a.id)
}

async function remove() {
  if (!article.value) return
  const a = article.value
  if (!(await confirm({ body: t('agent.common.confirmDelete', { name: a.title }), color: 'error', confirmText: t('agent.common.delete') }))) return
  // DELETE answers 204 with no body: return a marker so success is distinguishable from failure.
  const ok = await run(async () => (await unwrap(api.DELETE('/agent/kb/articles/{id}', { params: { path: { id: a.id } } })), true), t('agent.common.deleted'))
  if (ok !== undefined) void router.push('/agent/kb')
}
</script>

<template>
  <v-container fluid class="pa-4" style="max-width: 1100px">
    <PageHeader :title="article ? article.title : $t('agent.kb.new')" :subtitle="article ? `/${article.slug}` : $t('agent.kb.newHint')">
      <template #actions>
        <v-btn variant="text" :prepend-icon="mdiArrowLeft" to="/agent/kb">{{ $t('app.back') }}</v-btn>
        <template v-if="article">
          <v-chip size="small" label variant="tonal" :color="STATUS_COLOR[article.status]" data-testid="kb-status">{{ $t(`agent.kb.status.${article.status}`) }}</v-chip>
          <v-btn
            v-if="canKb(article, 'publish')"
            color="success"
            :prepend-icon="mdiPublish"
            :loading="busy"
            data-testid="kb-publish"
            @click="setStatus('published')"
          >{{ $t('agent.kb.publish') }}</v-btn>
          <v-btn
            v-if="canKb(article, 'unpublish')"
            variant="tonal"
            :prepend-icon="mdiPublishOff"
            :loading="busy"
            data-testid="kb-unpublish"
            @click="setStatus('draft')"
          >{{ $t('agent.kb.unpublish') }}</v-btn>
          <v-btn
            v-if="canKb(article, 'archive')"
            variant="tonal"
            :prepend-icon="mdiArchiveOutline"
            :loading="busy"
            data-testid="kb-archive"
            @click="setStatus('archived')"
          >{{ $t('agent.kb.archive') }}</v-btn>
          <v-btn
            v-if="canKb(article, 'delete')"
            variant="text"
            color="error"
            :prepend-icon="mdiDeleteOutline"
            data-testid="kb-delete"
            @click="remove"
          >{{ $t('agent.common.delete') }}</v-btn>
        </template>
      </template>
    </PageHeader>

    <v-alert v-if="failed" type="error" variant="tonal" class="mb-4" data-testid="kb-load-error">{{ failed }}</v-alert>
    <v-progress-linear v-if="loading" indeterminate class="mb-4" />
    <v-card v-else-if="!failed" border flat class="pa-4">
      <v-alert v-if="!categories.length" type="info" variant="tonal" density="compact" class="mb-3">{{ $t('agent.kb.noCategories') }}</v-alert>
      <KbArticleForm
        :key="formKey"
        :article="article"
        :translations="translations"
        :categories="categories"
        :locales="locales"
        :readonly="!!article && !canKb(article, 'edit')"
        @saved="onSaved"
        @translation-deleted="article && loadArticle(article.id)"
        @cancel="router.push('/agent/kb')"
      />
    </v-card>
  </v-container>
</template>
