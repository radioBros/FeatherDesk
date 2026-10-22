<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AuthCard from '../components/AuthCard.vue'
import InviteForm from '../forms/InviteForm.vue'
import MfaStep from '@/components/mfa/MfaStep.vue'
import type { MfaChallenge } from '@/components/mfa/mfa'

const route = useRoute()
const router = useRouter()
const challenge = ref<MfaChallenge | null>(null)
const token = computed(() => (typeof route.query.token === 'string' ? route.query.token : ''))
</script>

<template>
  <AuthCard :title="$t('portal.account.inviteTitle')" :subtitle="$t('portal.account.inviteIntro')">
    <v-alert v-if="!token" type="warning" variant="tonal" data-testid="invite-invalid">{{ $t('portal.account.linkInvalid') }}</v-alert>
    <MfaStep v-else-if="challenge" :challenge="challenge" @done="router.replace('/tickets')" @restart="router.replace('/login')" />
    <InviteForm v-else :token="token" @accepted="router.replace('/tickets')" @mfa="challenge = $event" />
  </AuthCard>
</template>
