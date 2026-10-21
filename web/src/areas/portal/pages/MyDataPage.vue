<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** `/profile/data` (FE-082, D-19 §C): the customer's export, download, deletion request and consents. */
import PageHeader from '@/components/PageHeader.vue'
import MyDataPanel from '@/components/privacy/MyDataPanel.vue'
</script>

<template>
  <v-container class="py-6" style="max-width: 860px">
    <PageHeader :title="$t('privacy.my.title')" :subtitle="$t('privacy.my.intro')" />
    <MyDataPanel allow-erasure />
  </v-container>
</template>
