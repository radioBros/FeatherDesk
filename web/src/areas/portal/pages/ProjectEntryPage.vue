<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { mdiFolderOffOutline } from '@mdi/js'
import { useConfigStore, type PortalProject } from '@/stores/config'
import { useSessionStore } from '@/stores/session'
import { useProjectBranding } from '../projectBranding'
import BrandLogo from '@/components/BrandLogo.vue'
import EmptyState from '@/components/EmptyState.vue'
import MarkdownView from '@/components/MarkdownView.vue'

/**
 * `/p/{key}` (D-20): a project's own portal entry. Shows its title, introduction,
 * logo and colour (null overrides fall back to the global branding); unknown or
 * not visible keys get a friendly "not found". Signed-in customers go straight
 * to the new-ticket form with the project preselected.
 */
const route = useRoute()
const router = useRouter()
const config = useConfigStore()
const session = useSessionStore()
const { enter } = useProjectBranding()

const key = computed(() => String(route.params.key ?? ''))
const project = ref<PortalProject | null>(null)
const loading = ref(true)
const newTicket = computed(() => `/tickets/new?project=${encodeURIComponent(project.value?.key ?? key.value)}`)
const auth = computed(() => config.config.customer)
const emailOn = computed(() => config.config.email_enabled)
const title = computed(() => project.value?.portal_title || project.value?.name || '')

watch(
  key,
  async (k) => {
    loading.value = true
    project.value = await enter(k)
    loading.value = false
    if (project.value) document.title = `${title.value} · ${config.branding.product_name}`
    if (project.value && session.isCustomer) void router.replace(newTicket.value)
  },
  { immediate: true },
)
</script>

<template>
  <v-container class="py-10" style="max-width: 640px">
    <v-progress-linear v-if="loading" indeterminate color="primary" />
    <EmptyState
      v-else-if="!project"
      :icon="mdiFolderOffOutline"
      :title="$t('portal.projects.notFoundTitle')"
      :body="$t('portal.projects.notFoundBody')"
      data-testid="project-not-found"
    >
      <v-btn to="/" color="primary" variant="flat">{{ $t('app.goHome') }}</v-btn>
    </EmptyState>
    <v-card v-else border flat class="pa-4 text-center fd-project-entry" data-testid="project-entry">
      <div class="mb-3"><BrandLogo :height="48" /></div>
      <h1 class="text-h4 mb-2" data-testid="project-title">{{ title }}</h1>
      <MarkdownView v-if="project.portal_intro_md" :source="project.portal_intro_md" class="text-start mb-4" data-testid="project-intro" />
      <p v-else-if="project.description" class="text-body-1 mb-4" data-testid="project-description">{{ project.description }}</p>
      <div class="d-flex flex-wrap justify-center ga-2">
        <v-btn v-if="session.isCustomer" :to="newTicket" color="primary" variant="flat" data-testid="project-new-ticket">{{ $t('portal.projects.newTicket') }}</v-btn>
        <template v-else>
          <v-btn :to="{ path: '/login', query: { redirect: newTicket } }" color="primary" variant="flat" data-testid="project-signin">{{ $t('auth.signIn') }}</v-btn>
          <v-btn v-if="auth.self_registration && emailOn" :to="{ path: '/register', query: { redirect: newTicket } }" data-testid="project-register">{{ $t('auth.register') }}</v-btn>
        </template>
      </div>
    </v-card>
  </v-container>
</template>

<style scoped>
.fd-project-entry { border-top: 4px solid rgb(var(--v-theme-primary)) !important; }
</style>
