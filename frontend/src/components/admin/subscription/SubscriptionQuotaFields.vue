<template>
  <div class="space-y-4">
    <div>
      <label class="input-label">{{ t('admin.groups.subscription.dailyLimit') }}</label>
      <input
        :value="formatInputValue(dailyLimitUsd)"
        type="number"
        step="0.01"
        min="0"
        :disabled="disabled"
        class="input"
        :placeholder="t('admin.groups.subscription.noLimit')"
        :data-testid="testId('daily-limit')"
        @input="onLimitInput('update:dailyLimitUsd', $event)"
      />
    </div>
    <div>
      <label class="input-label">{{ t('admin.groups.subscription.weeklyLimit') }}</label>
      <input
        :value="formatInputValue(weeklyLimitUsd)"
        type="number"
        step="0.01"
        min="0"
        :disabled="disabled"
        class="input"
        :placeholder="t('admin.groups.subscription.noLimit')"
        :data-testid="testId('weekly-limit')"
        @input="onLimitInput('update:weeklyLimitUsd', $event)"
      />
    </div>
    <div>
      <label class="input-label">{{ t('admin.groups.subscription.monthlyLimit') }}</label>
      <input
        :value="formatInputValue(monthlyLimitUsd)"
        type="number"
        step="0.01"
        min="0"
        :disabled="disabled"
        class="input"
        :placeholder="t('admin.groups.subscription.noLimit')"
        :data-testid="testId('monthly-limit')"
        @input="onLimitInput('update:monthlyLimitUsd', $event)"
      />
    </div>
    <div class="grid grid-cols-2 gap-3">
      <div>
        <label class="input-label">{{ t('admin.groups.subscription.hourlyLimit') }}</label>
        <input
          :value="formatInputValue(hourlyLimitUsd)"
          type="number"
          step="0.01"
          min="0"
          :disabled="disabled"
          class="input"
          :placeholder="t('admin.groups.subscription.noLimit')"
          :data-testid="testId('hourly-limit')"
          @input="onLimitInput('update:hourlyLimitUsd', $event)"
        />
      </div>
      <div>
        <label class="input-label">{{ t('admin.groups.subscription.hourlyWindowHours') }}</label>
        <input
          :value="hourlyWindowHours || 5"
          type="number"
          min="1"
          max="168"
          step="1"
          :disabled="disabled"
          class="input"
          :data-testid="testId('hourly-window-hours')"
          @input="onHoursInput($event)"
        />
      </div>
    </div>
    <p class="input-hint">
      {{ t('admin.groups.subscription.hourlyHint', { hours: hourlyWindowHours || 5 }) }}
    </p>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'

const props = withDefaults(
  defineProps<{
    dailyLimitUsd?: number | null
    weeklyLimitUsd?: number | null
    monthlyLimitUsd?: number | null
    hourlyLimitUsd?: number | null
    hourlyWindowHours?: number
    disabled?: boolean
    testIdPrefix?: string
  }>(),
  {
    dailyLimitUsd: null,
    weeklyLimitUsd: null,
    monthlyLimitUsd: null,
    hourlyLimitUsd: null,
    hourlyWindowHours: 5,
    disabled: false,
    testIdPrefix: ''
  }
)

const emit = defineEmits<{
  'update:dailyLimitUsd': [value: number | null]
  'update:weeklyLimitUsd': [value: number | null]
  'update:monthlyLimitUsd': [value: number | null]
  'update:hourlyLimitUsd': [value: number | null]
  'update:hourlyWindowHours': [value: number]
}>()

const { t } = useI18n()

function testId(suffix: string): string {
  return props.testIdPrefix ? `${props.testIdPrefix}-${suffix}` : suffix
}

function formatInputValue(val: number | null | undefined): string | number {
  if (val === null || val === undefined || Number.isNaN(val)) return ''
  return val
}

function emitLimit(
  eventName:
    | 'update:dailyLimitUsd'
    | 'update:weeklyLimitUsd'
    | 'update:monthlyLimitUsd'
    | 'update:hourlyLimitUsd',
  value: number | null
) {
  switch (eventName) {
    case 'update:dailyLimitUsd':
      emit('update:dailyLimitUsd', value)
      break
    case 'update:weeklyLimitUsd':
      emit('update:weeklyLimitUsd', value)
      break
    case 'update:monthlyLimitUsd':
      emit('update:monthlyLimitUsd', value)
      break
    case 'update:hourlyLimitUsd':
      emit('update:hourlyLimitUsd', value)
      break
  }
}

function onLimitInput(
  eventName:
    | 'update:dailyLimitUsd'
    | 'update:weeklyLimitUsd'
    | 'update:monthlyLimitUsd'
    | 'update:hourlyLimitUsd',
  event: Event
) {
  const raw = (event.target as HTMLInputElement).value.trim()
  if (raw === '') {
    emitLimit(eventName, null)
    return
  }
  const num = Number(raw)
  emitLimit(eventName, Number.isFinite(num) ? num : null)
}

function onHoursInput(event: Event) {
  const raw = (event.target as HTMLInputElement).value.trim()
  const parsed = Number.parseInt(raw, 10)
  emit('update:hourlyWindowHours', Number.isFinite(parsed) && parsed > 0 ? parsed : 5)
}
</script>
