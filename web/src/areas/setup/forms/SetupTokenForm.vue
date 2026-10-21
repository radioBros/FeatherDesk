<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { ref, watch } from 'vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'

/** Step 1: the setup token printed once in the container log (FE-049). */
const props = defineProps<{ token?: string; error?: string | null }>()
const emit = defineEmits<{ next: [token: string] }>()
const value = ref(props.token ?? '')
watch(() => props.token, (v) => v !== undefined && (value.value = v))
const { formRef, submit, fieldErrors, submitting } = useForm()

const onSubmit = submit(() => emit('next', value.value.trim()))
</script>

<template>
  <v-form ref="formRef" data-testid="setup-token-form" @submit.prevent="onSubmit">
    <p class="text-body-2 mb-4">{{ $t('setup.token.intro') }}</p>
    <v-alert v-if="error" type="error" variant="tonal" density="compact" class="mb-3" data-testid="setup-token-error">{{ error }}</v-alert>
    <v-text-field
      v-model="value"
      :label="$t('setup.token.label')"
      :hint="$t('setup.token.hint')"
      persistent-hint
      :rules="[validations.required.any, validations.setupToken]"
      :error-messages="fieldErrors.setup_token"
      autocomplete="off"
      spellcheck="false"
      class="mb-4"
      data-testid="setup-token"
    />
    <div class="d-flex justify-end">
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="setup-next">{{ $t('setup.next') }}</v-btn>
    </div>
  </v-form>
</template>
