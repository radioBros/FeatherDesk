<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { computed } from 'vue'
import { mdiKeyVariant, mdiDevices, mdiAccountGroupOutline, mdiShieldAccountOutline, mdiShieldKeyOutline } from '@mdi/js'
import { useSessionStore } from '@/stores/session'
import PageHeader from '@/components/PageHeader.vue'
import ThemeToggle from '@/components/ThemeToggle.vue'
import ProfileForm from '../forms/ProfileForm.vue'
import PasswordForm from '../forms/PasswordForm.vue'
import NotificationPrefsCard from '@/notifications/NotificationPrefsCard.vue'

/** `/profile` (FE-018): the password card exists only for accounts with a local password. */
const session = useSessionStore()
const hasPassword = computed(() => session.me?.has_password !== false)
</script>

<template>
  <v-container class="py-6" style="max-width: 760px">
    <PageHeader :title="$t('nav.profile')" />
    <v-card border flat class="pa-4 mb-4">
      <ProfileForm :key="session.me?.id" />
    </v-card>
    <NotificationPrefsCard area="portal" />
    <v-card v-if="hasPassword" border flat class="pa-4 mb-4" data-testid="password-section">
      <h2 class="text-h6 mb-3">{{ $t('portal.profile.changePassword') }}</h2>
      <PasswordForm />
    </v-card>
    <v-card border flat class="mb-4">
      <!-- A settings row and links, not a list of items (FE-032, axe aria-required-children). -->
      <v-list role="none">
        <v-list-item :title="$t('app.theme')" role="none">
          <template #append><ThemeToggle /></template>
        </v-list-item>
        <v-list-item v-if="hasPassword" to="/profile/security" :prepend-icon="mdiShieldKeyOutline" :title="$t('mfa.profile.title')" data-testid="profile-security-link" />
        <v-list-item to="/profile/tokens" :prepend-icon="mdiKeyVariant" :title="$t('nav.apiTokens')" data-testid="profile-tokens-link" />
        <v-list-item to="/profile/sessions" :prepend-icon="mdiDevices" :title="$t('portal.sessions.title')" data-testid="profile-sessions-link" />
        <v-list-item to="/profile/data" :prepend-icon="mdiShieldAccountOutline" :title="$t('privacy.my.link')" data-testid="profile-privacy-link" />
        <v-list-item
          v-if="session.isOrgManager"
          to="/organization/members"
          :prepend-icon="mdiAccountGroupOutline"
          :title="$t('portal.members.title')"
          data-testid="profile-members-link"
        />
      </v-list>
    </v-card>
  </v-container>
</template>
