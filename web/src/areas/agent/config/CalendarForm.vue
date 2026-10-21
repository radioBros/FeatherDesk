<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Create / edit a business calendar (FE-056): name, IANA time zone (from
 * `Intl`, no network), weekly hours (0..n intervals per day, HH:MM, end after
 * start and no overlap), holidays (unique ISO dates) and a "next business
 * minute" preview computed in the calendar's zone.
 */
import { computed, reactive } from 'vue'
import { mdiDelete, mdiPlus } from '@mdi/js'
import { useI18n } from 'vue-i18n'
import { useForm } from '@/composables/useForm'
import { validations, type TimeRange } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import type { components } from '@/api/schema'
import { WEEKDAYS, type Weekday } from './constants'
import { nextBusinessMinute, timeZones } from './calendar'

type S = components['schemas']
const props = defineProps<{ calendar?: S['BusinessCalendar'] | null }>()
const emit = defineEmits<{ saved: [S['BusinessCalendar']]; cancel: [] }>()
const { locale } = useI18n()

const defaultWeek = (): Record<Weekday, TimeRange[]> => ({
  mon: [{ start: '09:00', end: '17:00' }],
  tue: [{ start: '09:00', end: '17:00' }],
  wed: [{ start: '09:00', end: '17:00' }],
  thu: [{ start: '09:00', end: '17:00' }],
  fri: [{ start: '09:00', end: '17:00' }],
  sat: [],
  sun: [],
})
const initialWeek = (): Record<Weekday, TimeRange[]> => {
  if (!props.calendar) return defaultWeek()
  const w = props.calendar.week
  return Object.fromEntries(WEEKDAYS.map((d) => [d, (w[d] ?? []).map((r) => ({ ...r }))])) as Record<Weekday, TimeRange[]>
}
const browserZone = (() => {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || ''
  } catch {
    return ''
  }
})()

const model = reactive({
  name: props.calendar?.name ?? '',
  timezone: props.calendar?.timezone ?? (props.calendar ? '' : browserZone),
  week: initialWeek(),
  holidays: [...(props.calendar?.holidays ?? [])] as string[],
})
const zones = timeZones()

// Localised weekday names (2026-10-19 is a Monday), no translation keys needed.
const dayName = (i: number) =>
  new Intl.DateTimeFormat(locale.value, { weekday: 'long', timeZone: 'UTC' }).format(new Date(Date.UTC(2026, 9, 19 + i)))

const preview = computed(() => {
  if (validations.ianaTimeZone(model.timezone) !== true || !model.timezone) return null
  const next = nextBusinessMinute({ timezone: model.timezone, week: model.week, holidays: model.holidays })
  if (!next) return null
  return new Intl.DateTimeFormat(locale.value, { dateStyle: 'full', timeStyle: 'short', timeZone: model.timezone }).format(next)
})

const { formRef, submit, fieldErrors, formError, submitting } = useForm()

const onSave = submit(async () => {
  const body: S['CalendarCreate'] = {
    name: model.name.trim(),
    timezone: model.timezone.trim(),
    week: Object.fromEntries(WEEKDAYS.map((d) => [d, model.week[d].map((r) => ({ start: r.start, end: r.end }))])),
    holidays: [...model.holidays].sort(),
  }
  const saved = props.calendar
    ? await unwrap(api.PATCH('/admin/calendars/{id}', { params: { path: { id: props.calendar.id } }, body }))
    : await unwrap(api.POST('/admin/calendars', { body }))
  emit('saved', saved)
})
</script>

<template>
  <v-form ref="formRef" data-testid="calendar-form" @submit.prevent="onSave">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <v-text-field
      v-model="model.name"
      :label="$t('agent.common.name')"
      :rules="[validations.required.any, validations.maxLength(100)]"
      :error-messages="fieldErrors.name"
      data-testid="calendar-name"
    />
    <v-autocomplete
      v-model="model.timezone"
      :items="zones"
      :label="$t('agent.calendars.timezone')"
      :rules="[validations.required.any, validations.ianaTimeZone]"
      :error-messages="fieldErrors.timezone"
      data-testid="calendar-timezone"
    />

    <div class="text-subtitle-2 mt-2 mb-1">{{ $t('agent.calendars.weeklyHours') }}</div>
    <div v-for="(d, di) in WEEKDAYS" :key="d" class="d-flex flex-wrap align-start ga-2 py-1 st-day" :data-testid="`calendar-day-${d}`">
      <div class="st-dayname text-body-2 pt-2">{{ dayName(di) }}</div>
      <div class="flex-grow-1">
        <div v-if="!model.week[d].length" class="text-caption text-medium-emphasis pt-2">{{ $t('agent.calendars.closed') }}</div>
        <div v-for="(r, i) in model.week[d]" :key="i" class="d-flex align-start ga-2">
          <v-text-field
            v-model="r.start"
            :label="$t('agent.calendars.start')"
            placeholder="09:00"
            density="compact"
            :rules="[validations.required.any, validations.timeOfDay]"
            :data-testid="`calendar-${d}-start-${i}`"
          />
          <v-text-field
            v-model="r.end"
            :label="$t('agent.calendars.end')"
            placeholder="17:00"
            density="compact"
            :rules="[validations.required.any, validations.timeOfDay, validations.timeRangeOrder(() => model.week[d], i)]"
            :data-testid="`calendar-${d}-end-${i}`"
          />
          <v-btn :icon="mdiDelete" variant="text" size="small" :aria-label="$t('agent.common.remove')" @click="model.week[d].splice(i, 1)" />
        </div>
      </div>
      <v-btn :icon="mdiPlus" variant="text" size="small" :aria-label="$t('agent.calendars.addInterval')" :data-testid="`calendar-${d}-add`" @click="model.week[d].push({ start: '09:00', end: '17:00' })" />
    </div>

    <div class="text-subtitle-2 mt-4 mb-1">{{ $t('agent.calendars.holidays') }}</div>
    <div v-for="(_h, i) in model.holidays" :key="i" class="d-flex align-start ga-2">
      <v-text-field
        v-model="model.holidays[i]"
        :label="$t('agent.calendars.date')"
        type="date"
        density="compact"
        :rules="[validations.required.any, validations.date, validations.uniqueValues(() => model.holidays)]"
        :data-testid="`calendar-holiday-${i}`"
      />
      <v-btn :icon="mdiDelete" variant="text" size="small" :aria-label="$t('agent.common.remove')" @click="model.holidays.splice(i, 1)" />
    </div>
    <v-btn variant="tonal" size="small" :prepend-icon="mdiPlus" data-testid="calendar-holiday-add" @click="model.holidays.push('')">{{ $t('agent.calendars.addHoliday') }}</v-btn>

    <v-alert type="info" variant="tonal" density="compact" class="my-4" data-testid="calendar-preview">
      {{ preview ? $t('agent.calendars.nextBusinessMinute', { when: preview }) : $t('agent.calendars.noPreview') }}
    </v-alert>

    <div class="d-flex justify-end ga-2">
      <v-btn variant="text" @click="emit('cancel')">{{ $t('app.cancel') }}</v-btn>
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="calendar-save">{{ $t('agent.common.save') }}</v-btn>
    </div>
  </v-form>
</template>

<style scoped>
.st-dayname { width: 110px; }
.st-day { border-bottom: 1px solid rgba(var(--v-border-color), var(--v-border-opacity)); }
</style>
