<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute } from 'vue-router'
import { mdiEmailCheckOutline } from '@mdi/js'
import AuthCard from '../components/AuthCard.vue'
import RegisterForm from '../forms/RegisterForm.vue'

const sentTo = ref<string | null>(null)
// The `/p/{key}` entry sends people here with a redirect back to its new-ticket form: keep it for the sign-in links.
const route = useRoute()
const loginTo = computed(() => ({ path: '/login', query: route.query.redirect ? { redirect: String(route.query.redirect) } : {} }))
</script>

<template>
  <AuthCard :title="$t('portal.account.registerTitle')">
    <div v-if="sentTo" class="text-center" data-testid="register-done" role="status">
      <v-icon :icon="mdiEmailCheckOutline" size="48" color="success" class="mb-2" />
      <p class="mb-4">{{ $t('portal.account.checkInbox', { email: sentTo }) }}</p>
      <v-btn :to="loginTo" variant="text">{{ $t('portal.account.backToLogin') }}</v-btn>
    </div>
    <template v-else>
      <RegisterForm @registered="(e: string) => (sentTo = e)" />
      <div class="text-center mt-4 text-body-2">
        {{ $t('portal.account.haveAccount') }} <router-link :to="loginTo">{{ $t('auth.signIn') }}</router-link>
      </div>
    </template>
  </AuthCard>
</template>
