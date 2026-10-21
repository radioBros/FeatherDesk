<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'

export interface BrandingStep {
  product_name: string
  company_name: string
  primary_color: string
  logo: File | null
}

/**
 * Step 3: branding basics. Its Next creates the instance (POST /setup/complete,
 * done by the wizard through `complete`); the logo is uploaded right after,
 * once the new admin session exists.
 */
const props = defineProps<{
  initial?: Partial<BrandingStep>
  errors?: Record<string, string[]>
  complete?: (data: BrandingStep) => Promise<void>
}>()
const emit = defineEmits<{ back: [] }>()
const LOGO_TYPES = ['image/png', 'image/jpeg', 'image/webp', 'image/svg+xml'] as const
const LOGO_MAX = 1024 * 1024
const model = reactive({
  product_name: props.initial?.product_name ?? 'SimpleTickets',
  company_name: props.initial?.company_name ?? '',
  primary_color: props.initial?.primary_color ?? '#1A73E8',
})
const logo = ref<File | File[] | null>(null)
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

const onSubmit = submit(async () => {
  const file = Array.isArray(logo.value) ? (logo.value[0] ?? null) : logo.value
  await props.complete?.({ ...model, primary_color: model.primary_color.toUpperCase(), logo: file })
})
</script>

<template>
  <v-form ref="formRef" data-testid="setup-branding-form" @submit.prevent="onSubmit">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3" data-testid="form-error">{{ formError }}</v-alert>
    <v-text-field
      v-model="model.product_name"
      :label="$t('setup.branding.productName')"
      :rules="[validations.required.any, validations.maxLength(60)]"
      :error-messages="fieldErrors['branding.product_name'] ?? errors?.['branding.product_name']"
      data-testid="setup-product-name"
    />
    <v-text-field
      v-model="model.company_name"
      :label="$t('setup.branding.companyName')"
      :rules="[validations.maxLength(100)]"
      :error-messages="fieldErrors['branding.company_name'] ?? errors?.['branding.company_name']"
      data-testid="setup-company-name"
    />
    <v-text-field
      v-model="model.primary_color"
      :label="$t('setup.branding.primaryColor')"
      :rules="[validations.required.any, validations.hexColor]"
      :error-messages="fieldErrors['branding.primary_color'] ?? errors?.['branding.primary_color']"
      maxlength="7"
      data-testid="setup-primary-color"
    >
      <template #append-inner>
        <input
          v-model="model.primary_color"
          type="color"
          class="st-swatch"
          :aria-label="$t('setup.branding.pickColor')"
        />
      </template>
    </v-text-field>
    <v-file-input
      v-model="logo"
      :label="$t('setup.branding.logo')"
      :hint="$t('setup.branding.logoHint')"
      persistent-hint
      :accept="LOGO_TYPES.join(',')"
      :rules="[validations.fileType(LOGO_TYPES), validations.fileSize(LOGO_MAX)]"
      prepend-icon=""
      class="mb-4"
      data-testid="setup-logo"
    />
    <div class="d-flex justify-space-between">
      <v-btn variant="text" :disabled="submitting" @click="emit('back')">{{ $t('app.back') }}</v-btn>
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="setup-next">{{ $t('setup.branding.create') }}</v-btn>
    </div>
  </v-form>
</template>

<style scoped>
.st-swatch { width: 32px; height: 28px; border: 0; padding: 0; background: none; cursor: pointer; }
</style>
