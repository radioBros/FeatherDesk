<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** Create a personal API token (FE-030). The created token is emitted once and never stored here. */
import { computed, reactive } from 'vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import type { components } from '@/api/schema'

type S = components['schemas']
const props = defineProps<{ allowAdmin?: boolean }>()
const emit = defineEmits<{ created: [S['ApiTokenCreated']]; cancel: [] }>()

const BASE: S['Scope'][] = ['tickets:read', 'tickets:write', 'directory:read', 'directory:write', 'kb:read', 'kb:write']
const scopes = computed<S['Scope'][]>(() => (props.allowAdmin ? [...BASE, 'admin'] : BASE))
const model = reactive({ name: '', scopes: ['tickets:read'] as S['Scope'][], expires: '' })
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

function toggle(scope: S['Scope'], on: boolean | null) {
  model.scopes = on ? [...new Set([...model.scopes, scope])] : model.scopes.filter((s) => s !== scope)
}

const onCreate = submit(async () => {
  const body: S['TokenCreate'] = {
    name: model.name.trim(),
    scopes: model.scopes.filter((s) => scopes.value.includes(s)),
    expires_at: model.expires ? `${model.expires}T23:59:59Z` : null,
  }
  const created = await unwrap(api.POST('/me/tokens', { body }))
  emit('created', created)
})
</script>

<template>
  <v-form ref="formRef" data-testid="token-form" @submit.prevent="onCreate">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <v-text-field
      v-model="model.name"
      :label="$t('agent.common.name')"
      :hint="$t('agent.tokens.nameHint')"
      persistent-hint
      :rules="[validations.required.any, validations.maxLength(100)]"
      :error-messages="fieldErrors.name"
      data-testid="token-name"
    />
    <v-input :model-value="model.scopes" :rules="[validations.required.array]" :error-messages="fieldErrors.scopes" class="mt-3" data-testid="token-scopes">
      <div class="w-100">
        <div class="text-subtitle-2 mb-1">{{ $t('agent.tokens.scopes') }}</div>
        <v-checkbox
          v-for="s in scopes"
          :key="s"
          :model-value="model.scopes.includes(s)"
          :label="s"
          :hint="$t(`agent.tokens.scopeHints.${s.replace(':', '_')}`)"
          persistent-hint
          density="compact"
          :data-testid="`scope-${s}`"
          @update:model-value="(v: boolean | null) => toggle(s, v)"
        />
      </div>
    </v-input>
    <v-text-field
      v-model="model.expires"
      type="date"
      :label="$t('agent.tokens.expiry')"
      :hint="$t('agent.tokens.expiryHint')"
      persistent-hint
      :rules="[validations.date]"
      :error-messages="fieldErrors.expires_at"
      class="mt-3"
      data-testid="token-expiry"
    />
    <div class="d-flex justify-end ga-2 mt-3">
      <v-btn variant="text" @click="emit('cancel')">{{ $t('app.cancel') }}</v-btn>
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="token-create">{{ $t('agent.common.create') }}</v-btn>
    </div>
  </v-form>
</template>
