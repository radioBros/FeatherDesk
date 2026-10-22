<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import AutocompleteSearch from '@/components/AutocompleteSearch.vue'
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
import type { components, operations } from '@/api/schema'
import SectionIcon from '@/components/SectionIcon.vue'
import ColumnsMenu from '@/components/ColumnsMenu.vue'
import { useTableColumns } from '@/composables/useTableColumns'
import DataTable from '@/components/DataTable.vue'

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
type KbQuery = NonNullable<operations['listKbArticlesForEditor']['parameters']['query']>
/** Header sort; null = the API default (newest updated, or relevance with `q`). */
const sort = ref<NonNullable<KbQuery['sort']> | null>(null)
const categories = ref<KbCategory[]>([])
const locales = computed(() => editorLocales(config.config))

const list = useCursorList<Summary>((cursor) =>
  unwrap(
    api.GET('/agent/kb/articles', {
      params: {
        query: {
          cursor: cursor ?? undefined,
          sort: sort.value ?? undefined,
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

watch(sort, () => void list.reload())

onMounted(async () => {
  void list.reload()
  categories.value = await loadKbCategories().catch(() => [])
})

function open(a: Summary) {
  void router.push(`/agent/kb/articles/${a.id}`)
}
const covered = (a: Summary, l: string) => (a.available_locales ?? [a.locale]).includes(l as S['Locale'])
const rowProps = ({ item }: { item: Summary }) => ({
  'data-testid': 'kb-row',
  tabindex: 0,
  onKeydown: (e: KeyboardEvent) => {
    if (e.key === 'Enter') open(item)
  },
})

const cols = useTableColumns('kb-articles', () => [
  { key: 'title', label: t('agent.kb.titleCol'), locked: true, sort: { asc: 'title', desc: '-title' } },
  { key: 'category', label: t('agent.common.category'), lowPriority: true },
  { key: 'status', label: t('agent.common.status') },
  { key: 'visibility', label: t('agent.kb.visibility'), lowPriority: true },
  { key: 'locales', label: t('agent.kb.locales'), lowPriority: true },
  { key: 'updated', label: t('agent.common.updatedAt'), lowPriority: true, sort: { asc: 'updated_at', desc: '-updated_at' } },
])
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
      <AutocompleteSearch v-model="filters.status" :items="statusItems" :label="$t('agent.common.status')" density="compact" hide-details clearable style="max-width: 180px" data-testid="kb-filter-status" />
      <AutocompleteSearch v-model="filters.category_id" :items="categoryItems" :label="$t('agent.common.category')" density="compact" hide-details clearable style="max-width: 240px" />
      <AutocompleteSearch v-model="filters.locale" :items="locales.map((l) => ({ value: l, title: l.toUpperCase() }))" :label="$t('agent.kb.locale')" density="compact" hide-details clearable style="max-width: 140px" />
    </div>

    <v-alert v-if="list.error.value" type="error" variant="tonal" class="mb-4">{{ $t('agent.common.loadFailed') }}</v-alert>
    <v-card border flat>
      <v-progress-linear v-if="list.loading.value" indeterminate />
      <EmptyState v-if="!list.loading.value && !list.items.value.length" :title="$t('agent.kb.empty')" body="" />
      <template v-else>
        <ColumnsMenu :cols="cols" />
        <DataTable
          :cols="cols"
          :items="list.items.value"
          :loading="list.loading.value"
          server-sort
          :sort="sort"
          :has-more="!!list.next.value"
          density="comfortable"
          hover
          :row-props="rowProps"
          data-testid="kb-table"
          @update:sort="(v: string | null) => (sort = v as typeof sort)"
          @load-more="list.loadMore()"
          @click:row="(_e: unknown, { item }: { item: Summary }) => open(item)"
        >
          <template #[`item.title`]="{ item }">
            <div class="d-flex align-center ga-2">
              <SectionIcon section="kb" :icon="mdiFileDocumentOutline" :size="30" />
              <div>
                <router-link :to="`/agent/kb/articles/${item.id}`" class="text-decoration-none" @click.stop>{{ item.title }}</router-link>
                <div class="text-caption text-medium-emphasis">/{{ item.slug }}</div>
              </div>
            </div>
          </template>
          <template #[`item.category`]="{ item }">{{ categoryName(item.category_id) || '—' }}</template>
          <template #[`item.status`]="{ item }">
            <v-chip v-if="item.status" size="small" label variant="tonal" :color="STATUS_COLOR[item.status as S['KbStatus']]">{{ $t(`agent.kb.status.${item.status}`) }}</v-chip>
          </template>
          <template #[`item.visibility`]="{ item }">{{ item.visibility && VISIBILITIES.includes(item.visibility) ? $t(`agent.kb.vis.${item.visibility}`) : '—' }}</template>
          <template #[`item.locales`]="{ item }">
            <div class="d-flex flex-wrap ga-1" data-testid="kb-coverage">
              <v-chip
                v-for="l in locales"
                :key="l"
                size="x-small"
                label
                :variant="covered(item, l) ? 'flat' : 'outlined'"
                :color="covered(item, l) ? 'primary' : undefined"
                :title="covered(item, l) ? '' : $t('agent.kb.missingTranslation')"
              >{{ l.toUpperCase() }}</v-chip>
            </div>
          </template>
          <template #[`item.updated`]="{ item }"><RelativeTime :value="item.updated_at" /></template>
        </DataTable>
      </template>
    </v-card>
  </v-container>
</template>
