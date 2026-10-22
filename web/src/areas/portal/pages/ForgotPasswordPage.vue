<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { ref } from 'vue'
import AuthCard from '../components/AuthCard.vue'
import ForgotPasswordForm from '../forms/ForgotPasswordForm.vue'

const sent = ref(false)
</script>

<template>
  <AuthCard :title="$t('portal.account.forgotTitle')">
    <v-alert v-if="sent" type="info" variant="tonal" data-testid="forgot-done" role="status">
      {{ $t('portal.account.resetSent') }}
    </v-alert>
    <template v-else>
      <p class="text-body-2 mb-4">{{ $t('portal.account.forgotIntro') }}</p>
      <ForgotPasswordForm @sent="sent = true" />
    </template>
    <div class="text-center mt-4"><v-btn to="/login" variant="text">{{ $t('portal.account.backToLogin') }}</v-btn></div>
  </AuthCard>
</template>
