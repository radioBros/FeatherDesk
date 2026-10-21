<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * /agent/admin/kb-categories (FE-052, supervisor+): the category tree with
 * create / edit / delete and up/down ordering among siblings.
 */
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { mdiArrowDown, mdiArrowUp, mdiDeleteOutline, mdiPencil, mdiPlus } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import KbCategoryForm from './KbCategoryForm.vue'
import { api, unwrap } from '@/api/client'
import { useConfirm } from '@/composables/useConfirm'
import { localized } from '@/utils/localized'
import { useAction } from '../shared/useAction'
import { flattenTree, type KbCategory } from './kb'

const { t, locale } = useI18n()
const { confirm } = useConfirm()
const { run } = useAction()
const items = ref<KbCategory[]>([])
const loading = ref(true)
const failed = ref(false)
const dialog = ref(false)
const editing = ref<KbCategory | null>(null)

const label = (c: KbCategory) => localized(c.title, locale.value) || c.slug
const rows = computed(() => flattenTree(items.value, label))
const siblings = (c: KbCategory) =>
  rows.value.map((r) => r.item).filter((x) => (x.parent_id ?? null) === (c.parent_id ?? null))

async function load() {
  loading.value = true
  failed.value = false
  try {
    items.value = (await unwrap(api.GET('/admin/kb/categories'))) ?? []
  } catch {
    failed.value = true
  } finally {
    loading.value = false
  }
}
onMounted(load)

function openForm(c: KbCategory | null) {
  editing.value = c
  dialog.value = true
}
function onSaved() {
  dialog.value = false
  void load()
}

const fullBody = (c: KbCategory, sort: number) => ({ parent_id: c.parent_id ?? null, slug: c.slug, title: c.title, visibility: c.visibility, sort })

/** Swaps a category with its neighbour; renumbers the sibling group so sorts are distinct. */
async function move(c: KbCategory, dir: -1 | 1) {
  const list = siblings(c)
  const i = list.findIndex((x) => x.id === c.id)
  const j = i + dir
  if (i < 0 || j < 0 || j >= list.length) return
  const next = [...list]
  ;[next[i], next[j]] = [next[j]!, next[i]!]
  const changed = next.map((x, k) => ({ x, sort: (k + 1) * 10 })).filter(({ x, sort }) => x.sort !== sort)
  await run(async () => {
    for (const { x, sort } of changed)
      await unwrap(api.PATCH('/admin/kb/categories/{id}', { params: { path: { id: x.id } }, body: fullBody(x, sort) }))
  }, t('agent.common.saved'))
  await load()
}

async function remove(c: KbCategory) {
  if (!(await confirm({ body: t('agent.common.confirmDelete', { name: label(c) }), color: 'error', confirmText: t('agent.common.delete') }))) return
  // DELETE answers 204 with no body: return a marker so success is distinguishable from failure.
  const ok = await run(async () => (await unwrap(api.DELETE('/admin/kb/categories/{id}', { params: { path: { id: c.id } } })), true), t('agent.common.deleted'))
  if (ok !== undefined) await load()
}
</script>

<template>
  <v-container fluid class="pa-4">
    <PageHeader :title="$t('agent.admin.nav.kbCategories')" :subtitle="$t('agent.kbCategories.subtitle')">
      <template #actions>
        <v-btn color="primary" :prepend-icon="mdiPlus" data-testid="kbcat-new" @click="openForm(null)">{{ $t('agent.kbCategories.new') }}</v-btn>
      </template>
    </PageHeader>
    <v-alert v-if="failed" type="error" variant="tonal" class="mb-4">{{ $t('agent.common.loadFailed') }}</v-alert>
    <v-card border flat>
      <v-progress-linear v-if="loading" indeterminate />
      <EmptyState v-if="!loading && !rows.length" :title="$t('agent.kbCategories.empty')" body="" />
      <v-table v-else density="comfortable" data-testid="kbcat-table">
        <thead>
          <tr>
            <th>{{ $t('agent.kbCategories.title') }}</th>
            <th>{{ $t('agent.kb.slug') }}</th>
            <th>{{ $t('agent.kb.visibility') }}</th>
            <th>{{ $t('agent.kbCategories.articles') }}</th>
            <th class="text-right">{{ $t('agent.common.actions') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="{ item: c, depth } in rows" :key="c.id" data-testid="kbcat-row">
            <td :style="{ paddingLeft: `${16 + depth * 24}px` }">{{ label(c) }}</td>
            <td class="text-medium-emphasis">{{ c.slug }}</td>
            <td><v-chip size="small" label variant="tonal">{{ $t(`agent.kb.vis.${c.visibility}`) }}</v-chip></td>
            <td>{{ c.article_count ?? '—' }}</td>
            <td class="text-right text-no-wrap">
              <v-btn :icon="mdiArrowUp" variant="text" size="small" :aria-label="$t('agent.kbCategories.moveUp')" :disabled="siblings(c)[0]?.id === c.id" data-testid="kbcat-up" @click="move(c, -1)" />
              <v-btn :icon="mdiArrowDown" variant="text" size="small" :aria-label="$t('agent.kbCategories.moveDown')" :disabled="siblings(c).at(-1)?.id === c.id" data-testid="kbcat-down" @click="move(c, 1)" />
              <v-btn :icon="mdiPencil" variant="text" size="small" :aria-label="$t('agent.common.edit')" @click="openForm(c)" />
              <v-btn :icon="mdiDeleteOutline" variant="text" size="small" color="error" :aria-label="$t('agent.common.delete')" data-testid="kbcat-delete" @click="remove(c)" />
            </td>
          </tr>
        </tbody>
      </v-table>
    </v-card>

    <v-dialog v-model="dialog" max-width="560">
      <v-card :title="editing ? $t('agent.kbCategories.edit') : $t('agent.kbCategories.new')">
        <v-card-text>
          <KbCategoryForm v-if="dialog" :category="editing" :categories="items" @saved="onSaved" @cancel="dialog = false" />
        </v-card-text>
      </v-card>
    </v-dialog>
  </v-container>
</template>
