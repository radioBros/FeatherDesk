<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * The admin entries of the agent drawer, grouped, each with its section icon
 * and colour. A lazy chunk (the group is collapsed on first paint), so the
 * ~25 icons stay out of the agent's initial JS (FE-031 budget).
 */
import { mdiBellCogOutline } from '@mdi/js'
import { ADMIN_META as META } from '@/theme/routeSections'
import type { Section } from '@/theme/palette'
import NavHeader from '@/components/NavHeader.vue'
import NavItem from '@/components/NavItem.vue'

defineProps<{ groups: { group: string; items: { to: string; title: string }[] }[] }>()

const FALLBACK = { icon: mdiBellCogOutline, section: 'system' as Section }
const GROUP_SECTION: Record<string, Section> = { people: 'teams', support: 'tickets', channels: 'email', system: 'system' }
</script>

<template>
  <template v-for="g in groups" :key="g.group">
    <NavHeader :section="GROUP_SECTION[g.group] ?? 'system'" class="pl-8">{{ $t(`agent.nav.adminGroups.${g.group}`) }}</NavHeader>
    <NavItem
      v-for="i in g.items"
      :key="i.to"
      :to="i.to"
      :title="$t(i.title)"
      :icon="(META[i.to] ?? FALLBACK).icon"
      :section="(META[i.to] ?? FALLBACK).section"
      :size="24"
      class="pl-6"
      data-testid="nav-admin-item"
    />
  </template>
</template>
