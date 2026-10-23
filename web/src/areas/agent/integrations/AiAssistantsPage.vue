<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * /agent/admin/integrations/ai-assistants (D-21): the MCP switch (MCP-011),
 * moved here from Admin → Settings, and the way to the per-user connect helper.
 */
import { mdiArrowLeft, mdiKeyVariant, mdiRobotOutline } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import IntegrationsCard from '../settings/IntegrationsCard.vue'
</script>

<template>
  <v-container fluid class="pa-4" style="max-width: 960px">
    <v-btn variant="text" size="small" :prepend-icon="mdiArrowLeft" to="/agent/admin/integrations" class="mb-2 ml-n2">{{ $t('agent.integrations.breadcrumb') }}</v-btn>
    <PageHeader :title="$t('agent.integrations.cards.ai.name')" :subtitle="$t('agent.integrations.ai.subtitle')" section="email" :icon="mdiRobotOutline" />
    <IntegrationsCard />
    <v-card border flat class="pa-4 mt-4">
      <p class="text-body-2 mb-3">{{ $t('agent.integrations.ai.connectHint') }}</p>
      <v-btn variant="tonal" color="primary" :prepend-icon="mdiKeyVariant" to="/agent/profile/tokens">{{ $t('agent.integrations.ai.tokensLink') }}</v-btn>
    </v-card>
  </v-container>
</template>
