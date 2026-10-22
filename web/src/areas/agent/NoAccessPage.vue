<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** /agent/no-access (FE-044): the IdP account has no mapped support group (AUTH-006). */
import { mdiAccountCancelOutline } from '@mdi/js'
import { useRouter } from 'vue-router'
import EmptyState from '@/components/EmptyState.vue'
import { useSessionStore } from '@/stores/session'

const session = useSessionStore()
const router = useRouter()

async function signOut() {
  const redirect = await session.logout().catch(() => null)
  if (redirect && /^https?:\/\//.test(redirect)) window.location.assign(redirect)
  else await router.push('/agent/login')
}
</script>

<template>
  <v-app>
    <v-main>
      <v-container style="max-width: 640px">
        <EmptyState :icon="mdiAccountCancelOutline" :title="$t('agent.noAccess.title')" :body="$t('agent.noAccess.body')">
          <div class="d-flex flex-wrap justify-center ga-2">
            <v-btn v-if="session.me" color="primary" data-testid="no-access-signout" @click="signOut">{{ $t('auth.signOut') }}</v-btn>
            <v-btn v-else to="/agent/login" color="primary">{{ $t('auth.signIn') }}</v-btn>
            <v-btn to="/" variant="text" data-testid="no-access-portal">{{ $t('agent.noAccess.portal') }}</v-btn>
          </div>
        </EmptyState>
      </v-container>
    </v-main>
  </v-app>
</template>
