<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** `/profile/security` (FE-072): two-step verification for portal accounts with a local password. */
import PageHeader from '@/components/PageHeader.vue'
import MfaSecuritySection from '@/components/mfa/MfaSecuritySection.vue'
</script>

<template>
  <v-container class="py-6" style="max-width: 760px">
    <PageHeader :title="$t('mfa.profile.title')" />
    <MfaSecuritySection />
    <v-btn to="/profile" variant="text">{{ $t('app.back') }}</v-btn>
  </v-container>
</template>
