<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * "Insert KB article link" (FE-052). In a public reply the picker lists only
 * articles the requester may read (`public`, `customers`); internal articles
 * are offered only in internal notes. The composer additionally blocks a
 * public send that still contains an internal link (`validations.noInternalLinks`).
 */
import { computed, ref, watch } from 'vue'
import { mdiBookOpenPageVariantOutline } from '@mdi/js'
import { api, unwrap } from '@/api/client'
import type { components } from '@/api/schema'

type S = components['schemas']
const props = defineProps<{ mode: 'public' | 'internal' }>()
const emit = defineEmits<{ insert: [{ markdown: string; href: string; internal: boolean }] }>()
const open = ref(false)
const q = ref('')
const items = ref<S['KbArticleSummary'][]>([])
const loading = ref(false)
let timer: ReturnType<typeof setTimeout> | null = null

async function search() {
  loading.value = true
  try {
    const page = await unwrap(
      api.GET('/agent/kb/articles', { params: { query: { status: 'published', limit: 20, ...(q.value ? { q: q.value } : {}) } } }),
    )
    items.value = page.items ?? []
  } catch {
    items.value = []
  } finally {
    loading.value = false
  }
}
watch(open, (v) => v && void search())
watch(q, () => {
  if (timer) clearTimeout(timer)
  timer = setTimeout(search, 300)
})

/** Public replies never see internal articles (I-10 mirrored in the UI). */
const visible = computed(() =>
  props.mode === 'public' ? items.value.filter((a) => a.visibility === 'public' || a.visibility === 'customers') : items.value,
)

function pick(a: S['KbArticleSummary']) {
  const href = `/kb/a/${a.slug}`
  emit('insert', { markdown: `[${a.title.replace(/[[\]]/g, '')}](${href})`, href, internal: a.visibility === 'internal' })
  open.value = false
}
</script>

<template>
  <v-dialog v-model="open" max-width="520">
    <template #activator="{ props: p }">
      <v-btn v-bind="p" variant="text" size="small" :prepend-icon="mdiBookOpenPageVariantOutline" data-testid="kb-link-open">
        {{ $t('agent.ticket.composer.kbLink') }}
      </v-btn>
    </template>
    <v-card :title="$t('agent.ticket.composer.kbLinkTitle')">
      <v-card-text>
        <v-alert v-if="mode === 'public'" type="info" variant="tonal" density="compact" class="mb-3">
          {{ $t('agent.ticket.composer.kbPublicOnly') }}
        </v-alert>
        <v-text-field v-model="q" :label="$t('agent.common.search')" density="compact" autofocus hide-details data-testid="kb-link-search" />
        <v-progress-linear v-if="loading" indeterminate class="mt-2" />
        <v-list density="compact">
          <v-list-item v-for="a in visible" :key="a.id" :title="a.title" :subtitle="a.excerpt" data-testid="kb-link-item" @click="pick(a)">
            <template #append>
              <v-chip size="x-small" label :color="a.visibility === 'internal' ? 'warning' : undefined">
                {{ $t(`agent.ticket.kbVisibility.${a.visibility ?? 'public'}`) }}
              </v-chip>
            </template>
          </v-list-item>
          <v-list-item v-if="!loading && !visible.length" :title="$t('agent.common.noResults')" disabled />
        </v-list>
      </v-card-text>
      <v-card-actions><v-spacer /><v-btn @click="open = false">{{ $t('app.close') }}</v-btn></v-card-actions>
    </v-card>
  </v-dialog>
</template>
