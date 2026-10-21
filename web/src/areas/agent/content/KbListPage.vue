<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * /agent/kb (FE-052): every article the editor can see — drafts included —
 * with status, visibility and locale coverage. Filters go to the API.
 */
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { mdiFileDocumentOutline, mdiPlus } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import { api, unwrap } from '@/api/client'
import { useConfigStore } from '@/stores/config'
import { localized } from '@/utils/localized'
import { useCursorList } from '../shared/useCursorList'
import { editorLocales, loadKbCategories, VISIBILITIES, type KbCategory } from './kb'
import type { components } from '@/api/schema'
import SectionIcon from '@/components/SectionIcon.vue'

type S = components['schemas']
type Summary = S['KbArticleSummary'] & { available_locales?: string[] }

const router = useRouter()
const { t, locale } = useI18n()
const config = useConfigStore()
const filters = ref<{ q: string; status: S['KbStatus'] | null; category_id: string | null; locale: string | null }>({
  q: '',
  status: null,
  category_id: null,
  locale: null,
})
const categories = ref<KbCategory[]>([])
const locales = computed(() => editorLocales(config.config))

const list = useCursorList<Summary>((cursor) =>
  unwrap(
    api.GET('/agent/kb/articles', {
      params: {
        query: {
          cursor: cursor ?? undefined,
          q: filters.value.q.trim() || undefined,
          status: filters.value.status ?? undefined,
          category_id: filters.value.category_id ?? undefined,
          locale: (filters.value.locale as S['Locale'] | null) ?? undefined,
        },
      },
    }),
  ) as Promise<{ items: Summary[]; next_cursor: string | null }>,
)

const STATUS_COLOR: Record<S['KbStatus'], string> = { draft: 'secondary', published: 'success', archived: 'grey' }
const statusItems = computed(() => (['draft', 'published', 'archived'] as const).map((s) => ({ value: s, title: t(`agent.kb.status.${s}`) })))
const categoryItems = computed(() => categories.value.map((c) => ({ value: c.id, title: localized(c.title, locale.value) || c.slug })))
const categoryName = (id?: string) => categoryItems.value.find((c) => c.value === id)?.title ?? ''

let timer: ReturnType<typeof setTimeout> | null = null
watch(
  () => ({ ...filters.value }),
  (now, before) => {
    if (timer) clearTimeout(timer)
    // Debounce typing; other filters apply at once.
    timer = setTimeout(() => void list.reload(), now.q !== before.q ? 300 : 0)
  },
)

onMounted(async () => {
  void list.reload()
  categories.value = await loadKbCategories().catch(() => [])
})

function open(a: Summary) {
  void router.push(`/agent/kb/articles/${a.id}`)
}
</script>

<template>
  <v-container fluid class="pa-4">
    <PageHeader :title="$t('nav.knowledgeBase')" :subtitle="$t('agent.kb.subtitle')">
      <template #actions>
        <v-btn color="primary" :prepend-icon="mdiPlus" to="/agent/kb/articles/new" data-testid="kb-new">{{ $t('agent.kb.new') }}</v-btn>
      </template>
    </PageHeader>

    <div class="d-flex flex-wrap ga-3 mb-3" data-testid="kb-filters">
      <v-text-field v-model="filters.q" :label="$t('agent.common.search')" density="compact" hide-details clearable style="min-width: 220px" data-testid="kb-filter-q" @click:clear="filters.q = ''" />
      <v-select v-model="filters.status" :items="statusItems" :label="$t('agent.common.status')" density="compact" hide-details clearable style="max-width: 180px" data-testid="kb-filter-status" />
      <v-select v-model="filters.category_id" :items="categoryItems" :label="$t('agent.common.category')" density="compact" hide-details clearable style="max-width: 240px" />
      <v-select v-model="filters.locale" :items="locales.map((l) => ({ value: l, title: l.toUpperCase() }))" :label="$t('agent.kb.locale')" density="compact" hide-details clearable style="max-width: 140px" />
    </div>

    <v-alert v-if="list.error.value" type="error" variant="tonal" class="mb-4">{{ $t('agent.common.loadFailed') }}</v-alert>
    <v-card border flat>
      <v-progress-linear v-if="list.loading.value" indeterminate />
      <EmptyState v-if="!list.loading.value && !list.items.value.length" :title="$t('agent.kb.empty')" body="" />
      <v-table v-else density="comfortable" hover data-testid="kb-table">
        <thead>
          <tr>
            <th>{{ $t('agent.kb.titleCol') }}</th>
            <th>{{ $t('agent.common.category') }}</th>
            <th>{{ $t('agent.common.status') }}</th>
            <th>{{ $t('agent.kb.visibility') }}</th>
            <th>{{ $t('agent.kb.locales') }}</th>
            <th>{{ $t('agent.common.updatedAt') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="a in list.items.value" :key="a.id" class="st-row" data-testid="kb-row" tabindex="0" @click="open(a)" @keydown.enter="open(a)">
            <td>
              <div class="d-flex align-center ga-2">
                <SectionIcon section="kb" :icon="mdiFileDocumentOutline" :size="30" />
                <div>
                  <router-link :to="`/agent/kb/articles/${a.id}`" class="text-decoration-none" @click.stop>{{ a.title }}</router-link>
                  <div class="text-caption text-medium-emphasis">/{{ a.slug }}</div>
                </div>
              </div>
            </td>
            <td>{{ categoryName(a.category_id) || '—' }}</td>
            <td>
              <v-chip v-if="a.status" size="small" label variant="tonal" :color="STATUS_COLOR[a.status]">{{ $t(`agent.kb.status.${a.status}`) }}</v-chip>
            </td>
            <td>{{ a.visibility && VISIBILITIES.includes(a.visibility) ? $t(`agent.kb.vis.${a.visibility}`) : '—' }}</td>
            <td>
              <div class="d-flex flex-wrap ga-1" data-testid="kb-coverage">
                <v-chip
                  v-for="l in locales"
                  :key="l"
                  size="x-small"
                  label
                  :variant="(a.available_locales ?? [a.locale]).includes(l as S['Locale']) ? 'flat' : 'outlined'"
                  :color="(a.available_locales ?? [a.locale]).includes(l as S['Locale']) ? 'primary' : undefined"
                  :title="(a.available_locales ?? [a.locale]).includes(l as S['Locale']) ? '' : $t('agent.kb.missingTranslation')"
                >{{ l.toUpperCase() }}</v-chip>
              </div>
            </td>
            <td><RelativeTime :value="a.updated_at" /></td>
          </tr>
        </tbody>
      </v-table>
      <div v-if="list.next.value" class="pa-3 text-center">
        <v-btn variant="text" :loading="list.loading.value" data-testid="kb-more" @click="list.loadMore()">{{ $t('agent.common.loadMore') }}</v-btn>
      </div>
    </v-card>
  </v-container>
</template>

<style scoped>
.st-row { cursor: pointer; }
</style>
