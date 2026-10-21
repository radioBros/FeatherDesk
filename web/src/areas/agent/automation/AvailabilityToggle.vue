<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Agent user-menu entries (FE-060, D-19 A): the "away" switch
 * (`GET`/`PATCH /me/availability`; away agents are skipped by round-robin and
 * least-busy assignment) and the link to the personal macros. Rendered in the
 * shared UserMenu's slot by the agent layout only, so the portal never loads it.
 */
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { mdiLightningBolt } from '@mdi/js'
import { useSessionStore } from '@/stores/session'
import { useAction } from '../shared/useAction'
import { getAvailability, setAvailability } from './api'

const { t } = useI18n()
const session = useSessionStore()
const { run, busy } = useAction()
const away = ref(false)
const known = ref(false)

onMounted(async () => {
  if (!session.isStaff) return
  try {
    away.value = (await getAvailability()).away
    known.value = true
  } catch {
    /* the switch stays disabled */
  }
})

async function toggle(v: boolean | null) {
  const next = !!v
  const res = await run(() => setAvailability(next), next ? t('automation.availability.nowAway') : t('automation.availability.nowAvailable'))
  if (res) away.value = res.away
}
</script>

<template>
  <template v-if="session.isStaff">
    <v-list-item data-testid="availability" @click.stop>
      <v-switch
        :model-value="away"
        :label="$t('automation.availability.away')"
        :disabled="!known || busy"
        color="warning"
        density="compact"
        hide-details
        inset
        data-testid="availability-switch"
        @update:model-value="toggle"
      />
      <div class="text-caption text-medium-emphasis">{{ away ? $t('automation.availability.awayHint') : $t('automation.availability.availableHint') }}</div>
    </v-list-item>
    <v-list-item to="/agent/macros" :prepend-icon="mdiLightningBolt" :title="$t('automation.nav.myMacros')" data-testid="nav-my-macros" />
    <v-divider />
  </template>
</template>
