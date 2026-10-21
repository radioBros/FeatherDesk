<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * The one password input of the app: a v-text-field with an eye button that
 * shows / hides the value. Every prop, attribute, :rules, v-model and slot is
 * passed through. Raw `type="password"` is forbidden elsewhere (lint + test).
 */
import { computed, ref, useAttrs, useSlots } from 'vue'
import { mdiEye, mdiEyeOff } from '@mdi/js'

defineOptions({ inheritAttrs: false })

const attrs = useAttrs()
const slots = useSlots()
const visible = ref(false)
const passthroughSlots = computed(() => Object.keys(slots).filter((n) => n !== 'append-inner'))

function toggle() {
  visible.value = !visible.value
}
function onBlur(e: Event) {
  visible.value = false
  const h = attrs.onBlur as ((e: Event) => void) | ((e: Event) => void)[] | undefined
  if (Array.isArray(h)) h.forEach((f) => f(e))
  else h?.(e)
}
</script>

<template>
  <v-text-field v-bind="{ ...attrs, onBlur: undefined }" :type="visible ? 'text' : 'password'" @blur="onBlur">
    <template v-for="name in passthroughSlots" :key="name" #[name]="slotProps">
      <slot :name="name" v-bind="slotProps ?? {}" />
    </template>
    <template #append-inner>
      <slot name="append-inner" />
      <button
        type="button"
        class="password-field__toggle"
        :aria-label="visible ? $t('common.hidePassword') : $t('common.showPassword')"
        :aria-pressed="visible"
        data-testid="password-toggle"
        @mousedown.prevent
        @click="toggle"
      >
        <v-icon :icon="visible ? mdiEyeOff : mdiEye" size="small" />
      </button>
    </template>
  </v-text-field>
</template>

<style scoped>
.password-field__toggle {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  margin-inline-start: 4px;
  padding: 4px;
  border-radius: 50%;
  opacity: 0.7;
  cursor: pointer;
}
.password-field__toggle:hover,
.password-field__toggle:focus-visible {
  opacity: 1;
}
.password-field__toggle:focus-visible {
  outline: 2px solid currentColor;
}
</style>
