<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute } from 'vue-router'
import AuthCard from '../components/AuthCard.vue'
import ResetPasswordForm from '../forms/ResetPasswordForm.vue'

const route = useRoute()
const token = computed(() => (typeof route.query.token === 'string' ? route.query.token : ''))
const done = ref(false)
const expired = ref<string | null>(null)
</script>

<template>
  <AuthCard :title="$t('portal.account.resetTitle')">
    <div v-if="done" class="text-center" data-testid="reset-done" role="status">
      <p class="mb-4">{{ $t('portal.account.passwordChanged') }}</p>
      <v-btn to="/login" color="primary">{{ $t('auth.signIn') }}</v-btn>
    </div>
    <div v-else-if="expired !== null || !token" data-testid="reset-expired" role="alert">
      <v-alert type="warning" variant="tonal" class="mb-4">{{ expired || $t('portal.account.linkInvalid') }}</v-alert>
      <v-btn to="/forgot-password" color="primary" block data-testid="reset-new-link">{{ $t('portal.account.requestNewLink') }}</v-btn>
    </div>
    <ResetPasswordForm v-else :token="token" @done="done = true" @expired="(m: string) => (expired = m)" />
  </AuthCard>
</template>
