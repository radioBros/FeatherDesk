<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { absoluteTime, relativeTime } from '@/utils/time'

const props = defineProps<{ value: string | Date }>()
const { locale, t } = useI18n()
const now = ref(new Date())
// One timer per instance at 60 s — never sub-second (CONVENTIONS §10).
const timer = setInterval(() => (now.value = new Date()), 60_000)
onBeforeUnmount(() => clearInterval(timer))

const date = computed(() => (props.value instanceof Date ? props.value : new Date(props.value)))
const text = computed(() => relativeTime(date.value, now.value, locale.value) ?? t('time.justNow'))
const title = computed(() => absoluteTime(date.value, locale.value))
</script>

<template>
  <time :datetime="date.toISOString()" :title="title">{{ text }}</time>
</template>
