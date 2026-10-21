<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Hex colour input with a native swatch picker and a live WCAG contrast
 * badge (FE-047): white text on the colour (buttons, chips) and the colour as
 * text on a white surface are the same pair; below 4.5:1 it warns.
 */
import { computed } from 'vue'
import { validations, type Rule } from '@/constants/validations'
import { contrastRatio, normalizeHex } from '@/utils/color'

const props = withDefaults(defineProps<{ modelValue: string; label: string; required?: boolean; testid: string; contrast?: boolean }>(), { contrast: true })
const emit = defineEmits<{ 'update:modelValue': [string] }>()

const colorRules = computed<Rule[]>(() => (props.required ? [validations.required.any, validations.hexColor] : [validations.hexColor]))
const hex = computed(() => normalizeHex(props.modelValue ?? ''))
const ratio = computed(() => (hex.value ? contrastRatio(hex.value, '#ffffff') : null))
const low = computed(() => ratio.value !== null && ratio.value < 4.5)
const ratioText = computed(() => (ratio.value === null ? '' : ratio.value.toFixed(1)))
</script>

<template>
  <div class="d-flex align-start ga-2">
    <input
      type="color"
      class="st-swatch"
      :value="hex ?? '#000000'"
      :aria-label="label"
      :data-testid="`${testid}-swatch`"
      @input="(e) => emit('update:modelValue', (e.target as HTMLInputElement).value)"
    />
    <v-text-field
      :model-value="modelValue"
      :label="label"
      :rules="colorRules"
      placeholder="#1A73E8"
      density="compact"
      class="flex-grow-1"
      :data-testid="testid"
      @update:model-value="(v: string) => emit('update:modelValue', v)"
    >
      <template v-if="contrast && ratio !== null" #append-inner>
        <v-chip
          :color="low ? 'warning' : 'success'"
          size="x-small"
          label
          :data-testid="low ? `${testid}-contrast-warning` : `${testid}-contrast-ok`"
          :title="$t('agent.branding.contrastHint')"
        >
          {{ low ? $t('agent.branding.contrastLow', { ratio: ratioText }) : $t('agent.branding.contrastOk', { ratio: ratioText }) }}
        </v-chip>
      </template>
    </v-text-field>
  </div>
</template>

<style scoped>
.st-swatch { width: 40px; height: 40px; border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity)); border-radius: 4px; padding: 2px; background: none; cursor: pointer; flex: none; }
</style>
