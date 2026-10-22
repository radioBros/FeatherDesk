<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { mdiFileDocumentOutline } from '@mdi/js'
import RelativeTime from '@/components/RelativeTime.vue'
import type { KbArticleSummary } from '../kb'

defineProps<{ articles: KbArticleSummary[] }>()
</script>

<template>
  <!-- Links to articles: the items keep their link role (FE-032, axe aria-required-children). -->
  <v-list lines="two" class="py-0" role="none" data-testid="kb-article-list">
    <v-list-item
      v-for="a in articles"
      :key="a.id"
      :to="`/kb/a/${a.slug}`"
      :prepend-icon="mdiFileDocumentOutline"
      data-testid="kb-article-item"
    >
      <v-list-item-title class="text-wrap">{{ a.title }}</v-list-item-title>
      <v-list-item-subtitle>
        <span v-if="a.excerpt">{{ a.excerpt }} · </span><RelativeTime :value="a.updated_at" />
      </v-list-item-subtitle>
    </v-list-item>
  </v-list>
</template>
