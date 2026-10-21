<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import AutocompleteSearch from '@/components/AutocompleteSearch.vue'
/**
 * KB article editor form (FE-052): metadata (category, slug, visibility never
 * wider than the category, M-10) and one tab per available locale with title
 * and Markdown body. Save stores a draft for every staff member: POST on
 * create, then PATCH for changed metadata and PUT per changed translation.
 * A saved translation other than the article's default locale can be removed
 * (DELETE; the API refuses the default one). Read-only when the article's
 * `allowed_actions` lacks `edit`. Loads nothing on mount (FE-005 registry).
 */
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { mdiTranslateOff } from '@mdi/js'
import MarkdownEditor from '@/components/MarkdownEditor.vue'
import { useConfirm } from '@/composables/useConfirm'
import { useUiStore } from '@/stores/ui'
import { ApiProblem } from '@/api/problem'
import { useForm } from '@/composables/useForm'
import { validations, type Rule } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import { localized } from '@/utils/localized'
import { allowedVisibilities, type KbArticleWithActions, type KbCategory, type KbVisibility } from './kb'
import type { components } from '@/api/schema'

type S = components['schemas']
type Text = { title: string; body_md: string }

const props = withDefaults(
  defineProps<{
    article?: KbArticleWithActions | null
    translations?: Record<string, Text>
    categories?: KbCategory[]
    locales?: string[]
    readonly?: boolean
  }>(),
  { article: null, translations: () => ({}), categories: () => [], locales: () => ['en'], readonly: false },
)
const emit = defineEmits<{ saved: [KbArticleWithActions]; cancel: []; translationDeleted: [string] }>()
const { t, locale } = useI18n()
const { confirm } = useConfirm()
const ui = useUiStore()

const primary = computed(() => props.article?.locale ?? props.locales[0] ?? 'en')
const meta = reactive({
  category_id: props.article?.category_id ?? (null as string | null),
  slug: props.article?.slug ?? '',
  visibility: (props.article?.visibility ?? null) as KbVisibility | null,
})
const original: Record<string, Text> = Object.fromEntries(
  props.locales.map((l) => [l, { title: props.translations[l]?.title ?? '', body_md: props.translations[l]?.body_md ?? '' }]),
)
const texts = reactive<Record<string, Text>>(Object.fromEntries(props.locales.map((l) => [l, { ...original[l]! }])))
const tab = ref(primary.value)

const category = computed(() => props.categories.find((c) => c.id === meta.category_id) ?? null)
const visibilityItems = computed(() => allowedVisibilities(category.value).map((v) => ({ value: v, title: t(`agent.kb.vis.${v}`) })))
const categoryItems = computed(() =>
  props.categories.map((c) => ({ value: c.id, title: `${localized(c.title, locale.value) || c.slug} · ${t(`agent.kb.vis.${c.visibility}`)}` })),
)
// Narrow the visibility when the chosen category does not allow it (M-10).
watch(category, (c) => {
  if (!c) return
  const allowed = allowedVisibilities(c)
  if (!meta.visibility || !allowed.includes(meta.visibility)) meta.visibility = allowed[0] ?? null
})

const hasText = (l: string) => !!texts[l]?.title.trim()
const existed = (l: string) => !!original[l]?.title
const requiredTitle: Rule[] = [validations.required.any, validations.maxLength(200)]
const optionalTitle: Rule[] = [validations.maxLength(200)]
// The primary locale always needs a title; any other tab once it has content.
const titleRules = (l: string) => (l === primary.value || existed(l) || !!texts[l]?.body_md.trim() ? requiredTitle : optionalTitle)
const dirty = (l: string) => texts[l]!.title !== original[l]!.title || texts[l]!.body_md !== original[l]!.body_md

const { formRef, submit, fieldErrors, formError, submitting } = useForm()

// Only a saved translation in another locale than the article's own can go.
const canRemove = (l: string) => !!props.article && !props.readonly && l !== primary.value && existed(l)
const removing = ref<string | null>(null)
async function removeTranslation(l: string) {
  const a = props.article
  if (!a || !(await confirm({ body: t('agent.kb.confirmDeleteTranslation', { locale: l.toUpperCase() }), color: 'error', confirmText: t('agent.kb.deleteTranslation') }))) return
  removing.value = l
  try {
    await unwrap(api.DELETE('/agent/kb/articles/{id}/translations/{locale}', { params: { path: { id: a.id, locale: l as S['Locale'] } } }))
    original[l] = { title: '', body_md: '' }
    texts[l] = { title: '', body_md: '' }
    ui.notify(t('agent.kb.translationDeleted'), 'success')
    emit('translationDeleted', l)
  } catch (e) {
    ui.notify(e instanceof ApiProblem ? e.userMessage : t('agent.common.loadFailed'), 'error')
  } finally {
    removing.value = null
  }
}

const onSave = submit(async () => {
  let article: KbArticleWithActions
  const p = primary.value
  if (!props.article) {
    article = await unwrap(
      api.POST('/agent/kb/articles', {
        body: {
          category_id: meta.category_id!,
          locale: p as S['Locale'],
          title: texts[p]!.title.trim(),
          body_md: texts[p]!.body_md,
          slug: meta.slug.trim() || undefined,
          visibility: meta.visibility ?? undefined,
        },
      }),
    )
  } else {
    article = props.article
    const patch: S['KbArticlePatch'] = {}
    if (meta.category_id && meta.category_id !== props.article.category_id) patch.category_id = meta.category_id
    if (meta.slug.trim() && meta.slug.trim() !== props.article.slug) patch.slug = meta.slug.trim()
    if (meta.visibility && meta.visibility !== props.article.visibility) patch.visibility = meta.visibility
    if (Object.keys(patch).length)
      article = { ...article, ...(await unwrap(api.PATCH('/agent/kb/articles/{id}', { params: { path: { id: article.id } }, body: patch }))) }
  }
  for (const l of props.locales) {
    if (!props.article && l === p) continue
    if (!hasText(l) || !dirty(l)) continue
    await unwrap(
      api.PUT('/agent/kb/articles/{id}/translations/{locale}', {
        params: { path: { id: article.id, locale: l as S['Locale'] } },
        body: { title: texts[l]!.title.trim(), body_md: texts[l]!.body_md },
      }),
    )
    original[l] = { ...texts[l]! }
  }
  emit('saved', article)
})
</script>

<template>
  <v-form ref="formRef" :readonly="readonly" data-testid="kb-article-form" @submit.prevent="onSave">
    <v-alert v-if="readonly" type="info" variant="tonal" density="compact" class="mb-3" data-testid="kb-readonly">{{ $t('agent.kb.readOnly') }}</v-alert>
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <v-row density="compact">
      <v-col cols="12" md="5">
        <AutocompleteSearch
          v-model="meta.category_id"
          :items="categoryItems"
          :label="$t('agent.common.category')"
          :rules="[validations.required.select]"
          :error-messages="fieldErrors.category_id"
          data-testid="kb-category"
        />
      </v-col>
      <v-col cols="12" md="4">
        <v-text-field
          v-model="meta.slug"
          :label="$t('agent.kb.slug')"
          :hint="!article ? $t('agent.kb.slugHint') : undefined"
          :rules="[validations.kbSlug]"
          :error-messages="fieldErrors.slug"
          data-testid="kb-slug"
        />
      </v-col>
      <v-col cols="12" md="3">
        <AutocompleteSearch
          v-model="meta.visibility"
          :items="visibilityItems"
          :label="$t('agent.kb.visibility')"
          :rules="[validations.required.select]"
          :error-messages="fieldErrors.visibility"
          data-testid="kb-visibility"
        />
      </v-col>
    </v-row>

    <v-tabs v-model="tab" density="compact" class="mb-2" data-testid="kb-locale-tabs">
      <v-tab v-for="l in locales" :key="l" :value="l" :data-testid="`kb-tab-${l}`">
        {{ l.toUpperCase() }}
        <v-badge v-if="!hasText(l)" inline dot color="warning" class="ml-1" :title="$t('agent.kb.missingTranslation')" data-testid="kb-missing" />
      </v-tab>
    </v-tabs>
    <v-window v-model="tab">
      <v-window-item v-for="l in locales" :key="l" :value="l" eager>
        <div v-if="canRemove(l)" class="d-flex justify-end mb-1">
          <v-btn
            variant="text"
            size="small"
            color="error"
            :prepend-icon="mdiTranslateOff"
            :loading="removing === l"
            :data-testid="`kb-remove-${l}`"
            @click="removeTranslation(l)"
          >{{ $t('agent.kb.deleteTranslation') }}</v-btn>
        </div>
        <v-text-field
          v-model="texts[l]!.title"
          :label="$t('agent.kb.titleCol')"
          :rules="titleRules(l)"
          :error-messages="l === primary ? fieldErrors.title : undefined"
          :data-testid="`kb-title-${l}`"
        />
        <MarkdownEditor v-model="texts[l]!.body_md" :label="$t('agent.kb.body')" :rows="12" :error-messages="l === primary ? fieldErrors.body_md : undefined" />
      </v-window-item>
    </v-window>

    <div class="d-flex justify-end ga-2 mt-2">
      <v-btn variant="text" @click="emit('cancel')">{{ $t('app.cancel') }}</v-btn>
      <v-btn v-if="!readonly" type="submit" color="primary" :loading="submitting" data-testid="kb-save">
        {{ article && article.status !== 'draft' ? $t('agent.common.saveChanges') : $t('agent.kb.saveDraft') }}
      </v-btn>
    </div>
  </v-form>
</template>
