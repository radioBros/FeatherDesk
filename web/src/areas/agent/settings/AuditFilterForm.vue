<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Audit log filters (FE-042): action, actor (staff search), since date.
 * Applying validates first; the page turns the emitted filter into API params.
 */
import { reactive, ref } from 'vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import type { components } from '@/api/schema'
import type { AuditFilter } from './types'
const props = defineProps<{ filter?: Partial<AuditFilter> }>()
const emit = defineEmits<{ apply: [AuditFilter] }>()

const model = reactive<AuditFilter>({
  action: props.filter?.action ?? '',
  actor_id: props.filter?.actor_id ?? null,
  since: props.filter?.since ?? '',
})
const staff = ref<components['schemas']['StaffRef'][]>([])
const searching = ref(false)
let seq = 0
async function searchStaff(q: string) {
  if (!q || q.length < 2) return
  const mine = ++seq
  searching.value = true
  try {
    const res = await unwrap(api.GET('/agent/staff', { params: { query: { q } } }))
    if (mine === seq) staff.value = res ?? []
  } catch {
    /* keep the previous suggestions */
  } finally {
    if (mine === seq) searching.value = false
  }
}

const sinceRules = [validations.date]
const actionRules = [validations.maxLength(100)]
const { formRef, submit } = useForm()
const onApply = submit(() => emit('apply', { action: model.action.trim(), actor_id: model.actor_id, since: model.since }))
function clear() {
  model.action = ''
  model.actor_id = null
  model.since = ''
  emit('apply', { action: '', actor_id: null, since: '' })
}
</script>

<template>
  <v-form ref="formRef" data-testid="audit-filter-form" @submit.prevent="onApply">
    <v-row density="compact" align="center">
      <v-col cols="12" md="4">
        <v-text-field v-model="model.action" :label="$t('agent.audit.action')" :placeholder="$t('agent.audit.actionHint')" :rules="actionRules" density="compact" clearable data-testid="audit-action" />
      </v-col>
      <v-col cols="12" md="3">
        <v-autocomplete
          v-model="model.actor_id"
          :items="staff"
          item-title="name"
          item-value="id"
          :label="$t('agent.audit.actor')"
          :loading="searching"
          density="compact"
          clearable
          no-filter
          data-testid="audit-actor"
          @update:search="searchStaff"
        />
      </v-col>
      <v-col cols="12" md="3">
        <v-text-field v-model="model.since" :label="$t('agent.audit.since')" type="date" :rules="sinceRules" density="compact" data-testid="audit-since" />
      </v-col>
      <v-col cols="12" md="2" class="d-flex ga-2">
        <v-btn type="submit" color="primary" data-testid="audit-apply">{{ $t('agent.audit.apply') }}</v-btn>
        <v-btn variant="text" @click="clear">{{ $t('agent.common.clearFilters') }}</v-btn>
      </v-col>
    </v-row>
  </v-form>
</template>
