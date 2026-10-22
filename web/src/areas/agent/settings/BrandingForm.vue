<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import AutocompleteSearch from '@/components/AutocompleteSearch.vue'
import SelectionChips from '@/components/SelectionChips.vue'
/**
 * Branding settings form (FE-047): identity, colours (+ per-area overrides),
 * theme, login and portal texts, email look, legal links, locales. Emits the
 * draft on every change so the page's preview panes update without saving.
 */
import { computed, reactive, watch } from 'vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import { SUPPORTED_LOCALES } from '@/i18n'
import MarkdownEditor from '@/components/MarkdownEditor.vue'
import ColorField from './ColorField.vue'
import { toDraft, toSettings, type BrandingDraft, type BrandingSettings } from './branding'
import type { components } from '@/api/schema'

const props = defineProps<{ settings?: BrandingSettings | null }>()
const emit = defineEmits<{ saved: [components['schemas']['BrandingUpdateResult']]; draft: [BrandingDraft] }>()

const model = reactive<BrandingDraft>(toDraft(props.settings))
watch(model, () => emit('draft', model), { deep: true, immediate: true })

const LOCALE_NAMES: Record<string, string> = { en: 'English', it: 'Italiano', de: 'Deutsch', fr: 'Français', es: 'Español' }
const locales = SUPPORTED_LOCALES.map((l) => ({ value: l, title: LOCALE_NAMES[l] ?? l }))
const defaultLocales = computed(() => locales.filter((l) => model.available_locales.includes(l.value)))
watch(
  () => model.available_locales,
  (list) => {
    if (list.length && !list.includes(model.default_locale)) model.default_locale = list[0]!
  },
)
const FONTS = ['system', 'inter', 'roboto', 'source-sans', 'noto-sans'] as const

const nameRules = [validations.required.any, validations.maxLength(100)]
const textRules = [validations.maxLength(200)]
const urlRules = [validations.url]
const localeRules = [validations.required.array]
const cssRules = [validations.maxLength(20480)]

const { formRef, submit, fieldErrors, formError, submitting } = useForm()
const onSave = submit(async () => {
  const res = await unwrap(api.PUT('/admin/branding', { body: toSettings(model) }))
  emit('saved', res)
})
</script>

<template>
  <v-form ref="formRef" data-testid="branding-form" @submit.prevent="onSave">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>

    <section class="mb-6">
      <h2 class="text-subtitle-1 mb-3">{{ $t('agent.branding.identity') }}</h2>
      <v-text-field v-model="model.product_name" :label="$t('agent.branding.productName')" :rules="nameRules" :error-messages="fieldErrors.product_name" data-testid="brand-product-name" />
      <v-text-field v-model="model.company_name" :label="$t('agent.branding.companyName')" :rules="textRules" :error-messages="fieldErrors.company_name" />
      <v-text-field v-model="model.support_url" :label="$t('agent.branding.supportUrl')" :rules="urlRules" :error-messages="fieldErrors.support_url" placeholder="https://" data-testid="brand-support-url" />
    </section>

    <section class="mb-6">
      <h2 class="text-subtitle-1 mb-1">{{ $t('agent.branding.colors') }}</h2>
      <p class="text-body-2 text-medium-emphasis mb-3">{{ $t('agent.branding.colorsHint') }}</p>
      <ColorField v-model="model.colors.primary" :label="$t('agent.branding.primary')" required testid="brand-primary" />
      <ColorField v-model="model.colors.secondary" :label="$t('agent.branding.secondary')" testid="brand-secondary" />
      <ColorField v-model="model.colors.accent" :label="$t('agent.branding.accent')" testid="brand-accent" />

      <v-switch v-model="model.portal_override" :label="$t('agent.branding.portalOverride')" color="primary" inset hide-details data-testid="brand-portal-override" />
      <div v-if="model.portal_override" class="pl-4">
        <ColorField v-model="model.portal_colors.primary" :label="$t('agent.branding.primary')" required testid="brand-portal-primary" />
        <ColorField v-model="model.portal_colors.secondary" :label="$t('agent.branding.secondary')" testid="brand-portal-secondary" />
        <ColorField v-model="model.portal_colors.accent" :label="$t('agent.branding.accent')" testid="brand-portal-accent" />
      </div>
      <v-switch v-model="model.agent_override" :label="$t('agent.branding.agentOverride')" color="primary" inset hide-details data-testid="brand-agent-override" />
      <div v-if="model.agent_override" class="pl-4">
        <ColorField v-model="model.agent_colors.primary" :label="$t('agent.branding.primary')" required testid="brand-agent-primary" />
        <ColorField v-model="model.agent_colors.secondary" :label="$t('agent.branding.secondary')" testid="brand-agent-secondary" />
        <ColorField v-model="model.agent_colors.accent" :label="$t('agent.branding.accent')" testid="brand-agent-accent" />
      </div>
    </section>

    <section class="mb-6">
      <h2 class="text-subtitle-1 mb-3">{{ $t('agent.branding.look') }}</h2>
      <v-row density="compact">
        <v-col cols="12" sm="4">
          <AutocompleteSearch
            v-model="model.default_theme"
            :items="['light', 'dark', 'system'].map((v) => ({ value: v, title: $t(`agent.branding.theme.${v}`) }))"
            :label="$t('agent.branding.defaultTheme')"
          />
        </v-col>
        <v-col cols="12" sm="4">
          <AutocompleteSearch
            v-model="model.radius"
            :items="['sharp', 'default', 'rounded'].map((v) => ({ value: v, title: $t(`agent.branding.radius.${v}`) }))"
            :label="$t('agent.branding.radiusLabel')"
          />
        </v-col>
        <v-col cols="12" sm="4">
          <AutocompleteSearch v-model="model.font" :items="FONTS.map((v) => ({ value: v, title: $t(`agent.branding.fonts.${v}`) }))" :label="$t('agent.branding.font')" />
        </v-col>
      </v-row>
    </section>

    <section class="mb-6">
      <h2 class="text-subtitle-1 mb-3">{{ $t('agent.branding.loginPage') }}</h2>
      <v-text-field v-model="model.login_title" :label="$t('agent.branding.loginTitle')" :rules="textRules" data-testid="brand-login-title" />
      <v-text-field v-model="model.login_subtitle" :label="$t('agent.branding.loginSubtitle')" :rules="textRules" />
      <ColorField v-model="model.login_background_color" :label="$t('agent.branding.loginBackground')" :contrast="false" testid="brand-login-bg" />
    </section>

    <section class="mb-6">
      <h2 class="text-subtitle-1 mb-3">{{ $t('agent.branding.portalTexts') }}</h2>
      <MarkdownEditor v-model="model.welcome_md" :label="$t('agent.branding.welcome')" :rows="3" />
      <MarkdownEditor v-model="model.new_ticket_intro_md" :label="$t('agent.branding.newTicketIntro')" :rows="3" />
    </section>

    <section class="mb-6">
      <h2 class="text-subtitle-1 mb-3">{{ $t('agent.branding.emailLook') }}</h2>
      <ColorField v-model="model.email_header_color" :label="$t('agent.branding.emailHeader')" testid="brand-email-header" />
      <MarkdownEditor v-model="model.email_footer_md" :label="$t('agent.branding.emailFooter')" :rows="3" />
    </section>

    <section class="mb-6">
      <h2 class="text-subtitle-1 mb-3">{{ $t('agent.branding.legal') }}</h2>
      <v-text-field v-model="model.privacy_url" :label="$t('agent.branding.privacyUrl')" :rules="urlRules" :error-messages="fieldErrors['legal.privacy_url']" placeholder="https://" />
      <v-text-field v-model="model.terms_url" :label="$t('agent.branding.termsUrl')" :rules="urlRules" :error-messages="fieldErrors['legal.terms_url']" placeholder="https://" />
      <v-text-field v-model="model.imprint_url" :label="$t('agent.branding.imprintUrl')" :rules="urlRules" :error-messages="fieldErrors['legal.imprint_url']" placeholder="https://" />
      <v-switch v-model="model.powered_by" :label="$t('agent.branding.poweredBy')" color="primary" inset hide-details />
    </section>

    <section class="mb-6">
      <h2 class="text-subtitle-1 mb-3">{{ $t('agent.branding.languages') }}</h2>
      <AutocompleteSearch
        v-model="model.available_locales"
        :items="locales"
        :label="$t('agent.branding.availableLocales')"
        :rules="localeRules"
        multiple
        data-testid="brand-locales">
        <template #selection="{ item, index }"><SelectionChips :item="item" :index="index" :total="(model.available_locales ?? []).length" /></template>
      </AutocompleteSearch>
      <AutocompleteSearch v-model="model.default_locale" :items="defaultLocales" :label="$t('agent.branding.defaultLocale')" />
    </section>

    <section class="mb-6">
      <h2 class="text-subtitle-1 mb-1">{{ $t('agent.branding.customCss') }}</h2>
      <p class="text-body-2 text-medium-emphasis mb-3">{{ $t('agent.branding.customCssHint') }}</p>
      <v-textarea v-model="model.custom_css" :label="$t('agent.branding.customCss')" :rules="cssRules" rows="3" auto-grow class="fd-mono" />
    </section>

    <div class="d-flex justify-end">
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="branding-save">{{ $t('agent.common.saveChanges') }}</v-btn>
    </div>
  </v-form>
</template>

<style scoped>
.fd-mono :deep(textarea) { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 13px; }
</style>
