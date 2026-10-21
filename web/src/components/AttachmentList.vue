<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { mdiPaperclip } from '@mdi/js'
import type { Attachment } from '@/types'
import { formatBytes } from '@/constants/validations'

const props = defineProps<{ attachments: Attachment[]; area: 'portal' | 'agent' }>()
const href = (a: Attachment) => a.url ?? `/api/v1/${props.area}/attachments/${encodeURIComponent(a.id)}`
</script>

<template>
  <div v-if="attachments.length" class="d-flex flex-wrap ga-2 mt-2" data-testid="attachment-list">
    <v-chip
      v-for="a in attachments"
      :key="a.id"
      :href="href(a)"
      :prepend-icon="mdiPaperclip"
      size="small"
      variant="outlined"
      target="_blank"
      rel="noopener"
    >
      {{ a.filename }} <span class="text-medium-emphasis ml-1">({{ formatBytes(a.size_bytes) }})</span>
    </v-chip>
  </div>
</template>
