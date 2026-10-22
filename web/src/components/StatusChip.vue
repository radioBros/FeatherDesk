<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TicketStatus } from '@/types'
import { STATUS_COLOR } from '@/theme/palette'

/** Staff statuses plus the customer-facing `awaiting_reply` (CustomerStatus). */
type AnyStatus = TicketStatus | 'awaiting_reply'

const props = withDefaults(defineProps<{ status: AnyStatus; area?: 'portal' | 'agent'; size?: string }>(), {
  area: 'agent',
  size: 'small',
})
const { t } = useI18n()
const label = computed(() => t(`${props.area === 'portal' ? 'statusCustomer' : 'status'}.${props.status}`))
</script>

<template>
  <v-chip :color="STATUS_COLOR[status]" :size="size" variant="tonal" label data-testid="status-chip">{{ label }}</v-chip>
</template>
