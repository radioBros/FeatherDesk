<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { mdiMapMarkerQuestionOutline } from '@mdi/js'
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import EmptyState from './EmptyState.vue'

const route = useRoute()
const home = computed(() => (route.meta.area === 'agent' ? '/agent/tickets' : '/'))
</script>

<template>
  <v-container>
    <EmptyState :icon="mdiMapMarkerQuestionOutline" :title="$t('app.notFound')" :body="$t('app.notFoundHint')">
      <v-btn :to="home" color="primary" variant="flat">{{ $t('app.goHome') }}</v-btn>
    </EmptyState>
  </v-container>
</template>
