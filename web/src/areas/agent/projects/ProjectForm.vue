<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import AutocompleteSearch from '@/components/AutocompleteSearch.vue'
/**
 * Create / edit a project (D-20). The key is immutable once tickets exist
 * (`key_locked`); key, active and sort belong to supervisors and admins (a
 * project admin's PATCH never carries them). Edit sends only what changed.
 */
import { computed, reactive } from 'vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import type { components } from '@/api/schema'
import ColorField from '@/areas/agent/settings/ColorField.vue'

type S = components['schemas']
const props = defineProps<{
  project?: S['Project'] | null
  teams?: S['Team'][]
  /** Supervisors and admins: may set key, active and sort. */
  support?: boolean
}>()
const emit = defineEmits<{ saved: [S['Project']]; cancel: [] }>()

const ICONS = ['mdi-web', 'mdi-cellphone', 'mdi-lifebuoy', 'mdi-server', 'mdi-cart-outline', 'mdi-folder-outline', 'mdi-bug-outline', 'mdi-account-group-outline']
const p = props.project
const model = reactive({
  key: p?.key ?? '',
  name: p?.name ?? '',
  description: p?.description ?? '',
  color: p?.color ?? '',
  icon: p?.icon ?? '',
  active: p?.active ?? true,
  sort: String(p?.sort ?? 0),
  default_team_id: p?.default_team_id ?? null,
  portal_visible: p?.portal_visible ?? true,
  portal_title: p?.portal_title ?? '',
  portal_intro_md: p?.portal_intro_md ?? '',
  primary_color: p?.primary_color ?? '',
})
const keyDisabled = computed(() => !!p && (p.key_locked || !props.support))
const teamItems = computed(() => {
  const mapped = p ? new Set(p.team_ids) : null
  return (props.teams ?? []).filter((t) => !mapped || mapped.has(t.id)).map((t) => ({ value: t.id, title: t.name }))
})
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

const orNull = (v: string) => (v.trim() === '' ? null : v)

const onSave = submit(async () => {
  const sort = Number(model.sort) || 0
  const fields = {
    name: model.name.trim(),
    description: model.description,
    color: model.color.trim(),
    icon: model.icon ?? '',
    default_team_id: model.default_team_id,
    portal_visible: model.portal_visible,
    portal_title: orNull(model.portal_title),
    portal_intro_md: orNull(model.portal_intro_md),
    primary_color: orNull(model.primary_color),
  }
  let saved: S['Project']
  if (!p) {
    saved = await unwrap(
      api.POST('/admin/projects', {
        body: { key: model.key.trim().toUpperCase(), ...fields, icon: fields.icon || undefined, active: model.active, sort },
      }),
    )
  } else {
    const body: S['ProjectPatch'] = {}
    const before = fields as Record<string, unknown>
    const current: Record<string, unknown> = {
      name: p.name, description: p.description, color: p.color, icon: p.icon, default_team_id: p.default_team_id,
      portal_visible: p.portal_visible, portal_title: p.portal_title, portal_intro_md: p.portal_intro_md, primary_color: p.primary_color,
    }
    for (const k of Object.keys(before)) if (before[k] !== current[k]) (body as Record<string, unknown>)[k] = before[k]
    if (props.support) {
      if (!p.key_locked && model.key.trim().toUpperCase() !== p.key) body.key = model.key.trim().toUpperCase()
      if (model.active !== p.active) body.active = model.active
      if (sort !== p.sort) body.sort = sort
    }
    saved = await unwrap(api.PATCH('/admin/projects/{id}', { params: { path: { id: p.id } }, body }))
  }
  emit('saved', saved)
})
</script>

<template>
  <v-form ref="formRef" data-testid="project-form" @submit.prevent="onSave">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <v-row dense>
      <v-col cols="12" sm="4">
        <v-text-field
          v-model="model.key"
          :label="$t('projects.admin.key')"
          :hint="project?.key_locked ? $t('projects.admin.keyLocked') : $t('projects.admin.keyHint')"
          persistent-hint
          :disabled="keyDisabled"
          :rules="[validations.required.any, validations.projectKey]"
          :error-messages="fieldErrors.key"
          density="compact"
          data-testid="project-key"
        />
      </v-col>
      <v-col cols="12" sm="8">
        <v-text-field
          v-model="model.name"
          :label="$t('agent.common.name')"
          :rules="[validations.required.any, validations.maxLength(100)]"
          :error-messages="fieldErrors.name"
          density="compact"
          data-testid="project-name"
        />
      </v-col>
    </v-row>
    <v-textarea
      v-model="model.description"
      :label="$t('agent.common.description')"
      :rules="[validations.maxLength(1000)]"
      :error-messages="fieldErrors.description"
      rows="2"
      auto-grow
      density="compact"
      data-testid="project-description"
    />
    <v-row dense>
      <v-col cols="12" sm="6">
        <ColorField v-model="model.color" :label="$t('projects.admin.color')" testid="project-color" :contrast="false" />
      </v-col>
      <v-col cols="12" sm="6">
        <AutocompleteSearch
          v-model="model.icon"
          :items="ICONS"
          :label="$t('projects.admin.icon')"
          :rules="[validations.maxLength(64)]"
          :error-messages="fieldErrors.icon"
          clearable
          density="compact"
          data-testid="project-icon"
        />
      </v-col>
    </v-row>
    <AutocompleteSearch
      v-model="model.default_team_id"
      :items="teamItems"
      :label="$t('projects.admin.defaultTeam')"
      :hint="project ? $t('projects.admin.defaultTeamHint') : $t('projects.admin.defaultTeamNew')"
      persistent-hint
      clearable
      :error-messages="fieldErrors.default_team_id"
      density="compact"
      class="mb-2"
      data-testid="project-default-team"
    />
    <div v-if="support" class="d-flex flex-wrap align-center ga-4">
      <v-switch v-model="model.active" :label="$t('agent.common.active')" color="primary" density="compact" hide-details data-testid="project-active" />
      <v-text-field
        v-model="model.sort"
        :label="$t('projects.admin.sort')"
        inputmode="numeric"
        :rules="[validations.integer]"
        :error-messages="fieldErrors.sort"
        density="compact"
        hide-details="auto"
        style="max-width: 140px"
        data-testid="project-sort"
      />
    </div>
    <div class="text-subtitle-2 mt-4 mb-1">{{ $t('projects.admin.portalSection') }}</div>
    <v-switch v-model="model.portal_visible" :label="$t('projects.admin.portalVisible')" color="primary" density="compact" hide-details data-testid="project-portal-visible" />
    <v-text-field
      v-model="model.portal_title"
      :label="$t('projects.admin.portalTitle')"
      :hint="$t('projects.admin.portalFallback')"
      persistent-hint
      :rules="[validations.maxLength(200)]"
      :error-messages="fieldErrors.portal_title"
      density="compact"
      class="mb-2"
      data-testid="project-portal-title"
    />
    <v-textarea
      v-model="model.portal_intro_md"
      :label="$t('projects.admin.portalIntro')"
      :rules="[validations.maxLength(5000)]"
      :error-messages="fieldErrors.portal_intro_md"
      rows="3"
      auto-grow
      density="compact"
      data-testid="project-portal-intro"
    />
    <ColorField v-model="model.primary_color" :label="$t('projects.admin.primaryColor')" testid="project-primary-color" />
    <div class="d-flex justify-end ga-2 mt-3">
      <v-btn variant="text" @click="emit('cancel')">{{ $t('app.cancel') }}</v-btn>
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="project-save">{{ $t('agent.common.save') }}</v-btn>
    </div>
  </v-form>
</template>
