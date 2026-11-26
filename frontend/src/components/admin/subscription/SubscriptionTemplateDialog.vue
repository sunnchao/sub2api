<template>
  <BaseDialog
    :show="show"
    :title="template ? t('admin.subscriptions.templates.editTitle') : t('admin.subscriptions.templates.createTitle')"
    width="normal"
    :show-close-button="!saving"
    :close-on-escape="!saving"
    @close="emit('close')"
  >
    <form id="subscription-template-form" class="space-y-4" @submit.prevent="handleSubmit">
      <div>
        <label class="input-label">
          {{ t('admin.subscriptions.templates.name') }} <span class="text-red-500">*</span>
        </label>
        <input
          v-model="form.name"
          type="text"
          maxlength="120"
          required
          :disabled="saving"
          class="input"
          :placeholder="t('admin.subscriptions.templates.namePlaceholder')"
          data-testid="template-name-input"
        />
      </div>

      <div>
        <label class="input-label">{{ t('admin.subscriptions.templates.description') }}</label>
        <textarea
          v-model="form.description"
          rows="2"
          :disabled="saving"
          class="input"
          :placeholder="t('admin.subscriptions.templates.descriptionPlaceholder')"
        />
      </div>

      <div>
        <label class="input-label">
          {{ t('admin.subscriptions.templates.boundGroup') }} <span class="text-red-500">*</span>
        </label>
        <Select
          v-model="form.group_id"
          :disabled="saving"
          :options="groupOptions"
          :placeholder="t('admin.subscriptions.templates.selectGroup')"
          data-testid="template-group-select"
        >
          <template #selected="{ option }">
            <GroupBadge
              v-if="option"
              :name="(option as unknown as GroupOption).label"
              :platform="(option as unknown as GroupOption).platform"
              :subscription-type="(option as unknown as GroupOption).subscriptionType"
              :rate-multiplier="(option as unknown as GroupOption).rate"
            />
            <span v-else class="text-gray-400">{{ t('admin.subscriptions.templates.selectGroup') }}</span>
          </template>
          <template #option="{ option, selected }">
            <GroupOptionItem
              :name="(option as unknown as GroupOption).label"
              :platform="(option as unknown as GroupOption).platform"
              :subscription-type="(option as unknown as GroupOption).subscriptionType"
              :rate-multiplier="(option as unknown as GroupOption).rate"
              :description="(option as unknown as GroupOption).description"
              :selected="selected"
            />
          </template>
        </Select>
        <p class="input-hint">{{ t('admin.subscriptions.templates.boundGroupHint') }}</p>
      </div>

      <div class="border-l-2 border-primary-200 pl-4 dark:border-primary-800">
        <SubscriptionQuotaFields
          v-model:daily-limit-usd="form.daily_limit_usd"
          v-model:weekly-limit-usd="form.weekly_limit_usd"
          v-model:monthly-limit-usd="form.monthly_limit_usd"
          v-model:hourly-limit-usd="form.hourly_limit_usd"
          v-model:hourly-window-hours="form.hourly_window_hours"
          :disabled="saving"
          test-id-prefix="template"
        />
      </div>

      <div class="grid grid-cols-2 gap-4">
        <div>
          <label class="input-label">{{ t('admin.subscriptions.templates.defaultValidityDays') }}</label>
          <input
            v-model.number="form.default_validity_days"
            type="number"
            min="1"
            max="36500"
            step="1"
            required
            :disabled="saving"
            class="input"
            data-testid="template-validity-input"
          />
        </div>
        <div>
          <label class="input-label">{{ t('admin.subscriptions.templates.sortOrder') }}</label>
          <input
            v-model.number="form.sort_order"
            type="number"
            step="1"
            :disabled="saving"
            class="input"
          />
        </div>
      </div>

      <div>
        <label class="input-label">{{ t('admin.subscriptions.templates.status') }}</label>
        <Select v-model="form.status" :disabled="saving" :options="statusOptions" />
      </div>

      <p v-if="template" class="rounded-lg bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:bg-amber-900/20 dark:text-amber-300">
        {{ t('admin.subscriptions.templates.snapshotNotice') }}
      </p>
    </form>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary" :disabled="saving" @click="emit('close')">
          {{ t('common.cancel') }}
        </button>
        <button
          type="submit"
          form="subscription-template-form"
          class="btn btn-primary"
          :disabled="saving"
          data-testid="template-save-btn"
        >
          {{ saving ? t('common.saving') : t('common.save') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { subscriptionTemplatesAPI, type SubscriptionTemplate } from '@/api/admin/subscriptionTemplates'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { Group, GroupPlatform, SubscriptionType } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import GroupBadge from '@/components/common/GroupBadge.vue'
import GroupOptionItem from '@/components/common/GroupOptionItem.vue'
import SubscriptionQuotaFields from './SubscriptionQuotaFields.vue'

interface GroupOption {
  [key: string]: unknown
  value: number
  label: string
  description: string | null
  platform: GroupPlatform
  subscriptionType: SubscriptionType
  rate: number
}

const props = defineProps<{
  show: boolean
  template: SubscriptionTemplate | null
  groups: Group[]
}>()

const emit = defineEmits<{
  close: []
  saved: []
}>()

const { t } = useI18n()
const appStore = useAppStore()
const saving = ref(false)

const form = reactive({
  name: '',
  description: '',
  group_id: null as number | null,
  daily_limit_usd: null as number | null,
  weekly_limit_usd: null as number | null,
  monthly_limit_usd: null as number | null,
  hourly_limit_usd: null as number | null,
  hourly_window_hours: 5,
  default_validity_days: 30,
  status: 'active' as 'active' | 'inactive',
  sort_order: 0
})

const groupOptions = computed<GroupOption[]>(() =>
  props.groups
    .filter((g) => g.status === 'active')
    .map((g) => ({
      value: g.id,
      label: g.name,
      description: g.description ?? null,
      platform: g.platform,
      subscriptionType: g.subscription_type,
      rate: g.rate_multiplier
    }))
)

const statusOptions = computed(() => [
  { value: 'active', label: t('admin.subscriptions.status.active') },
  { value: 'inactive', label: t('admin.subscriptions.templates.inactive') }
])

watch(
  () => props.show,
  (visible) => {
    if (!visible) return
    if (props.template) {
      form.name = props.template.name
      form.description = props.template.description || ''
      form.group_id = props.template.group_id
      form.daily_limit_usd = props.template.daily_limit_usd ?? null
      form.weekly_limit_usd = props.template.weekly_limit_usd ?? null
      form.monthly_limit_usd = props.template.monthly_limit_usd ?? null
      form.hourly_limit_usd = props.template.hourly_limit_usd ?? null
      form.hourly_window_hours = props.template.hourly_window_hours || 5
      form.default_validity_days = props.template.default_validity_days || 30
      form.status = props.template.status || 'active'
      form.sort_order = props.template.sort_order || 0
    } else {
      form.name = ''
      form.description = ''
      form.group_id = null
      form.daily_limit_usd = null
      form.weekly_limit_usd = null
      form.monthly_limit_usd = null
      form.hourly_limit_usd = null
      form.hourly_window_hours = 5
      form.default_validity_days = 30
      form.status = 'active'
      form.sort_order = 0
    }
  }
)

function normalizeLimit(val: number | null): number | null {
  if (val === null || val === undefined || Number.isNaN(val) || val <= 0) return null
  return val
}

async function handleSubmit() {
  const trimmedName = form.name.trim()
  if (!trimmedName) {
    appStore.showError(t('admin.subscriptions.templates.nameRequired'))
    return
  }
  if (!form.group_id) {
    appStore.showError(t('admin.subscriptions.pleaseSelectGroup'))
    return
  }
  saving.value = true
  try {
    const payload = {
      name: trimmedName,
      description: form.description.trim(),
      group_id: form.group_id,
      daily_limit_usd: normalizeLimit(form.daily_limit_usd),
      weekly_limit_usd: normalizeLimit(form.weekly_limit_usd),
      monthly_limit_usd: normalizeLimit(form.monthly_limit_usd),
      hourly_limit_usd: normalizeLimit(form.hourly_limit_usd),
      hourly_window_hours: Number(form.hourly_window_hours) || 5,
      default_validity_days: Number(form.default_validity_days) || 30,
      status: form.status,
      sort_order: Number(form.sort_order) || 0
    }
    if (props.template) {
      await subscriptionTemplatesAPI.update(props.template.id, payload)
    } else {
      await subscriptionTemplatesAPI.create(payload)
    }
    appStore.showSuccess(t('common.saved'))
    emit('close')
    emit('saved')
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    saving.value = false
  }
}
</script>
