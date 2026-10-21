<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * `/agent/profile/data` (FE-083, D-19 §C): staff self-service export and download.
 * Staff accounts are erased by an administrator, never by self-request.
 */
import PageHeader from '@/components/PageHeader.vue'
import MyDataPanel from '@/components/privacy/MyDataPanel.vue'
</script>

<template>
  <v-container fluid class="pa-4" style="max-width: 960px">
    <PageHeader :title="$t('privacy.my.title')" :subtitle="$t('privacy.my.introStaff')" />
    <MyDataPanel />
  </v-container>
</template>
