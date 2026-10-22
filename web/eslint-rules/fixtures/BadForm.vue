<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
// Deliberately violates CONVENTIONS §12 and FE-007; linted by lint-fixtures.spec.ts.
import { ref } from 'vue'
import { useSessionStore } from '@/stores/session'
const session = useSessionStore()
const name = ref('')
const isBoss = session.me?.role === 'admin'
async function save() {
  await fetch('/api/v1/x', { method: 'POST' })
}
</script>

<template>
  <v-form @submit.prevent="save">
    <v-text-field v-model="name" :rules="[(v: string) => !!v || 'required']" />
    <span v-if="isBoss">boss</span>
  </v-form>
</template>
