<template>
  <BaseDialog
    :show="show"
    :title="t('admin.subscriptions.editLimits.title')"
    width="normal"
    :show-close-button="!saving"
    :close-on-escape="!saving"
    @close="emit('close')"
  >
    <form v-if="subscription" id="edit-subscription-limits-form" class="space-y-4" @submit.prevent="handleSubmit">
      <div class="rounded-lg bg-gray-50 p-3 text-sm dark:bg-dark-700">
        <div class="font-medium text-gray-900 dark:text-white">
          {{ subscription.user?.email || `#${subscription.user_id}` }}
        </div>
        <div class="mt-1 flex flex-wrap items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
          <span v-if="subscription.template_name" class="badge badge-primary">
            {{ subscription.template_name }}
          </span>
          <span>{{ subscription.group?.name || `#${subscription.group_id}` }}</span>
        </div>
      </div>

      <SubscriptionQuotaFields
        v-model:daily-limit-usd="form.daily_limit_usd"
        v-model:weekly-limit-usd="form.weekly_limit_usd"
        v-model:monthly-limit-usd="form.monthly_limit_usd"
        v-model:hourly-limit-usd="form.hourly_limit_usd"
        v-model:hourly-window-hours="form.hourly_window_hours"
        :disabled="saving"
        test-id-prefix="sub-limits"
      />

      <p class="text-xs text-gray-500 dark:text-gray-400">
        {{ t('admin.subscriptions.editLimits.hint') }}
      </p>
    </form>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary" :disabled="saving" @click="emit('close')">
          {{ t('common.cancel') }}
        </button>
        <button
          type="submit"
          form="edit-subscription-limits-form"
          class="btn btn-primary"
          :disabled="saving"
          data-testid="save-sub-limits-btn"
        >
          {{ saving ? t('common.saving') : t('common.save') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import adminAPI from '@/api/admin'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { UserSubscription } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import SubscriptionQuotaFields from './SubscriptionQuotaFields.vue'

const props = defineProps<{
  show: boolean
  subscription: UserSubscription | null
}>()

const emit = defineEmits<{
  close: []
  saved: []
}>()

const { t } = useI18n()
const appStore = useAppStore()
const saving = ref(false)

const form = reactive({
  daily_limit_usd: null as number | null,
  weekly_limit_usd: null as number | null,
  monthly_limit_usd: null as number | null,
  hourly_limit_usd: null as number | null,
  hourly_window_hours: 5
})

watch(
  () => [props.show, props.subscription] as const,
  ([visible, sub]) => {
    if (!visible || !sub) return
    if (sub.has_custom_limits) {
      form.daily_limit_usd = sub.daily_limit_usd ?? null
      form.weekly_limit_usd = sub.weekly_limit_usd ?? null
      form.monthly_limit_usd = sub.monthly_limit_usd ?? null
      form.hourly_limit_usd = sub.hourly_limit_usd ?? null
      form.hourly_window_hours = sub.hourly_window_hours || 5
    } else {
      form.daily_limit_usd = sub.group?.daily_limit_usd ?? null
      form.weekly_limit_usd = sub.group?.weekly_limit_usd ?? null
      form.monthly_limit_usd = sub.group?.monthly_limit_usd ?? null
      form.hourly_limit_usd = sub.group?.hourly_limit_usd ?? null
      form.hourly_window_hours = sub.group?.hourly_window_hours || 5
    }
  },
  { immediate: true }
)

function normalizeLimit(val: number | null): number | null {
  if (val === null || val === undefined || Number.isNaN(val) || val <= 0) return null
  return val
}

async function handleSubmit() {
  if (!props.subscription) return
  saving.value = true
  try {
    await adminAPI.subscriptions.updateLimits(props.subscription.id, {
      daily_limit_usd: normalizeLimit(form.daily_limit_usd),
      weekly_limit_usd: normalizeLimit(form.weekly_limit_usd),
      monthly_limit_usd: normalizeLimit(form.monthly_limit_usd),
      hourly_limit_usd: normalizeLimit(form.hourly_limit_usd),
      hourly_window_hours: Number(form.hourly_window_hours) || 5
    })
    appStore.showSuccess(t('admin.subscriptions.editLimits.saved'))
    emit('close')
    emit('saved')
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    saving.value = false
  }
}
</script>
