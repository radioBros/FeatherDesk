<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { mdiCheckCircleOutline, mdiPauseCircleOutline, mdiAlertCircleOutline, mdiClockAlertOutline, mdiClockOutline } from '@mdi/js'
import RelativeTime from '@/components/RelativeTime.vue'
import { slaState, type SlaState } from './sla'

const props = defineProps<{ label: string; start: string; due?: string | null; metAt?: string | null; pausedAt?: string | null; breached?: boolean }>()
const now = ref(new Date())
// 60 s refresh, never sub-second (CONVENTIONS §10).
const timer = setInterval(() => (now.value = new Date()), 60_000)
onBeforeUnmount(() => clearInterval(timer))

const state = computed(() =>
  slaState({ start: props.start, due: props.due, metAt: props.metAt, pausedAt: props.pausedAt, breached: props.breached, now: now.value }),
)
const META: Record<SlaState, { color: string; icon: string }> = {
  met: { color: 'success', icon: mdiCheckCircleOutline },
  paused: { color: 'secondary', icon: mdiPauseCircleOutline },
  breached: { color: 'error', icon: mdiAlertCircleOutline },
  warning: { color: 'warning', icon: mdiClockAlertOutline },
  ok: { color: 'info', icon: mdiClockOutline },
  none: { color: 'grey', icon: mdiClockOutline },
}
</script>

<template>
  <v-chip v-if="state !== 'none'" :color="META[state].color" :prepend-icon="META[state].icon" size="small" variant="tonal" label :data-testid="`sla-${state}`">
    {{ label }} · {{ $t(`agent.ticket.sla.${state}`) }}
    <template v-if="due && (state === 'ok' || state === 'warning' || state === 'breached')">
      &nbsp;(<RelativeTime :value="due" />)
    </template>
  </v-chip>
</template>
