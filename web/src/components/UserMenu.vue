<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { mdiAccountCircleOutline, mdiKeyVariant, mdiLogout, mdiThemeLightDark, mdiTranslate } from '@mdi/js'
import { useRouter } from 'vue-router'
import { useSessionStore } from '@/stores/session'
import UserAvatar from './UserAvatar.vue'
import NavItem from './NavItem.vue'
import { useBarCollapsed, useLocalePicker, useThemeMode } from '@/composables/useAppPrefs'

const props = defineProps<{ area: 'portal' | 'agent' }>()
const session = useSessionStore()
const router = useRouter()
// On a phone the bar's theme and language buttons move in here.
const collapsed = useBarCollapsed()
const { modes, set: setTheme } = useThemeMode()
const { locale, available, names, pick } = useLocalePicker()
const base = props.area === 'agent' ? '/agent' : ''

async function signOut() {
  const redirect = await session.logout().catch(() => null)
  // RP-initiated logout (AUTH-007): the IdP end-session URL when the session came from OIDC.
  if (redirect && /^https?:\/\//.test(redirect)) window.location.assign(redirect)
  else await router.push(props.area === 'agent' ? '/agent/login' : '/login')
}
</script>

<template>
  <v-menu v-if="session.me">
    <template #activator="{ props: p }">
      <v-btn v-bind="p" icon variant="text" :aria-label="$t('nav.userMenu')" data-testid="user-menu">
        <UserAvatar :name="session.me.name" :size="32" />
      </v-btn>
    </template>
    <v-list density="compact" min-width="220">
      <v-list-item :title="session.me.name" :subtitle="session.me.email" />
      <v-divider />
      <!-- Area-specific entries (agent: availability + macros, FE-060). -->
      <slot />
      <NavItem :to="`${base}/profile`" :icon="mdiAccountCircleOutline" section="customers" :title="$t('nav.profile')" />
      <NavItem :to="`${base}/profile/tokens`" :icon="mdiKeyVariant" section="system" :title="$t('nav.apiTokens')" />
      <template v-if="collapsed">
        <v-list-group value="theme" data-testid="menu-theme">
          <template #activator="{ props: g }">
            <NavItem v-bind="g" :icon="mdiThemeLightDark" section="branding" :title="$t('app.theme')" @click.stop />
          </template>
          <NavItem v-for="m in modes" :key="m.value" :icon="m.icon" section="branding" :size="26" :title="$t(m.label)" @click="setTheme(m.value)" />
        </v-list-group>
        <v-list-group value="language" data-testid="menu-language">
          <template #activator="{ props: g }">
            <NavItem v-bind="g" :icon="mdiTranslate" section="email" :title="`${$t('app.language')} (${locale.toUpperCase()})`" @click.stop />
          </template>
          <v-list-item v-for="l in available" :key="l" :title="names[l]" :active="l === locale" @click="pick(l)" />
        </v-list-group>
      </template>
      <NavItem :icon="mdiLogout" section="danger" :title="$t('auth.signOut')" data-testid="sign-out" @click="signOut" />
    </v-list>
  </v-menu>
</template>
