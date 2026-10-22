<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { mdiArrowDown, mdiArrowUp, mdiChevronDoubleUp, mdiMinus } from '@mdi/js'
import type { Priority } from '@/types'
import { PRIORITY_COLOR } from '@/theme/palette'

withDefaults(defineProps<{ priority: Priority; size?: string }>(), { size: 'small' })
const META: Record<Priority, { color: string; icon: string }> = {
  low: { color: PRIORITY_COLOR.low, icon: mdiArrowDown },
  normal: { color: PRIORITY_COLOR.normal, icon: mdiMinus },
  high: { color: PRIORITY_COLOR.high, icon: mdiArrowUp },
  urgent: { color: PRIORITY_COLOR.urgent, icon: mdiChevronDoubleUp },
}
</script>

<template>
  <v-chip :color="META[priority].color" :size="size" variant="tonal" label :prepend-icon="META[priority].icon" data-testid="priority-chip">
    {{ $t(`priority.${priority}`) }}
  </v-chip>
</template>
