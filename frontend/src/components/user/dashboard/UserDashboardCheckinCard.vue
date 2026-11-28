<template>
  <div class="card">
    <div class="flex items-center justify-between border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <div class="flex items-center gap-2">
        <div class="flex h-8 w-8 items-center justify-center rounded-lg bg-amber-100 text-amber-600 dark:bg-amber-900/30 dark:text-amber-300">
          <Icon name="gift" size="sm" />
        </div>
        <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
          {{ t('checkin.card.title') }}
        </h2>
      </div>
      <router-link
        to="/checkin"
        class="text-xs font-medium text-primary-600 hover:underline dark:text-primary-400"
      >
        {{ t('checkin.card.viewDetails') }}
      </router-link>
    </div>

    <div class="space-y-4 p-5">
      <div v-if="loading" class="flex items-center justify-center py-6">
        <LoadingSpinner size="md" />
      </div>

      <template v-else-if="status">
        <!-- Today's Status -->
        <div
          v-if="status.checked_in_today && status.today_record"
          class="rounded-xl border border-emerald-200 bg-emerald-50/70 p-3.5 dark:border-emerald-500/30 dark:bg-emerald-900/20"
        >
          <div class="flex items-center justify-between gap-2">
            <span class="inline-flex items-center gap-1.5 text-sm font-semibold text-emerald-700 dark:text-emerald-300">
              <Icon name="checkCircle" size="sm" />
              {{ t('checkin.card.checkedInToday') }}
            </span>
            <span class="font-mono text-sm font-bold text-emerald-700 dark:text-emerald-300">
              +${{ formatAmount(status.today_record.total_amount) }}
            </span>
          </div>
          <p class="mt-1 text-xs text-emerald-600/90 dark:text-emerald-300/80">
            {{ t('checkin.card.expiresHint', { time: formatDateTimeToMinute(status.today_record.expires_at) }) }}
          </p>
        </div>

        <div
          v-else
          class="rounded-xl border border-gray-200 bg-gray-50 p-3.5 dark:border-dark-600 dark:bg-dark-700/50"
        >
          <div class="flex items-center justify-between gap-2">
            <span class="text-xs font-medium text-gray-500 dark:text-gray-400">
              {{ t('checkin.card.notCheckedIn') }}
            </span>
            <span class="text-sm font-semibold text-primary-600 dark:text-primary-400">
              {{ rewardPreviewText }}
            </span>
          </div>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            {{ t('checkin.card.validityHint', { days: status.reward_preview.validity_days }) }}
          </p>
        </div>

        <!-- Streak & Next Milestone -->
        <div class="grid grid-cols-2 gap-3 text-xs">
          <div class="rounded-lg bg-gray-50 px-3 py-2 dark:bg-dark-700/40">
            <p class="text-gray-500 dark:text-gray-400">{{ t('checkin.card.streakLabel') }}</p>
            <p class="mt-0.5 flex items-center gap-1 text-sm font-semibold text-gray-900 dark:text-white">
              <Icon name="fire" size="sm" class="text-amber-500" />
              {{ t('checkin.card.streakDays', { days: status.current_streak }) }}
            </p>
          </div>
          <div class="rounded-lg bg-gray-50 px-3 py-2 dark:bg-dark-700/40">
            <p class="text-gray-500 dark:text-gray-400">{{ t('checkin.card.nextMilestoneLabel') }}</p>
            <p v-if="status.next_milestone" class="mt-0.5 text-sm font-semibold text-gray-900 dark:text-white">
              {{
                t('checkin.card.nextMilestoneIn', {
                  days: nextMilestoneRemainingDays,
                  bonus: formatAmount(status.next_milestone.bonus_amount)
                })
              }}
            </p>
            <p v-else class="mt-0.5 text-sm font-medium text-gray-400 dark:text-gray-500">
              {{ t('checkin.card.noMilestone') }}
            </p>
          </div>
        </div>

        <!-- Ineligible Notice -->
        <div
          v-if="!status.checked_in_today && !status.eligibility.eligible"
          data-testid="checkin-card-ineligible"
          class="rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-200"
        >
          {{ eligibilityMessage }}
        </div>

        <!-- Check-in CTA -->
        <button
          type="button"
          data-testid="quick-checkin-btn"
          class="btn btn-primary w-full"
          :disabled="ctaDisabled"
          @click="handleCheckinClick"
        >
          <Icon name="sparkles" size="sm" class="mr-1.5" />
          {{ ctaLabel }}
        </button>
      </template>

      <div v-else class="py-4 text-center text-xs text-gray-500 dark:text-gray-400">
        {{ t('checkin.errors.default') }}
      </div>
    </div>

    <CheckinCaptchaDialog
      ref="captchaDialogRef"
      :show="showCaptchaModal"
      :submitting="checkingIn"
      @close="showCaptchaModal = false"
      @submit="handleCaptchaSubmit"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import CheckinCaptchaDialog from '@/components/user/checkin/CheckinCaptchaDialog.vue'
import { checkinAPI } from '@/api/checkin'
import { useAppStore } from '@/stores'
import { extractI18nErrorMessage } from '@/utils/apiError'
import { formatDateTimeToMinute } from '@/utils/format'
import type { ActionCaptchaRequestProof, CheckinExecuteResponse, CheckinStatus } from '@/types'

const emit = defineEmits<{
  checkedIn: [result?: CheckinExecuteResponse]
}>()

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(false)
const checkingIn = ref(false)
const showCaptchaModal = ref(false)
const status = ref<CheckinStatus | null>(null)
const captchaDialogRef = ref<InstanceType<typeof CheckinCaptchaDialog> | null>(null)

const amountPrecision = computed(() => {
  const raw = Number(status.value?.reward_preview?.amount_precision ?? 2)
  if (!Number.isFinite(raw)) return 2
  return Math.min(4, Math.max(2, Math.trunc(raw)))
})

function formatAmount(value: number, precision = amountPrecision.value): string {
  const num = Number(value)
  const p = Math.min(4, Math.max(2, Number.isFinite(precision) ? Math.trunc(precision) : 2))
  if (!Number.isFinite(num)) return (0).toFixed(p)
  return num.toFixed(p)
}

const rewardPreviewText = computed(() => {
  const preview = status.value?.reward_preview
  if (!preview) return '$0.00'
  const precision = amountPrecision.value
  if (preview.reward_mode === 'random') {
    return `$${formatAmount(preview.min_amount, precision)} ~ $${formatAmount(preview.max_amount, precision)}`
  }
  return `$${formatAmount(preview.base_amount, precision)}`
})

const nextMilestoneRemainingDays = computed(() => {
  const next = status.value?.next_milestone
  if (!next) return 0
  const cycleDay = status.value?.milestone_cycle_day ?? 0
  const maxDays = (status.value?.milestones ?? []).reduce((m, r) => Math.max(m, r.days), 0)
  if (
    status.value?.milestone_cycle_repeat &&
    (next.days < cycleDay || (maxDays > 0 && cycleDay >= maxDays && next.days <= cycleDay))
  ) {
    return next.days
  }
  return Math.max(0, next.days - cycleDay)
})

const eligibilityMessage = computed(() => {
  const elig = status.value?.eligibility
  if (!elig || elig.eligible) return ''
  if (elig.reason_code) {
    const key = `checkin.errors.${elig.reason_code}`
    const translated = t(key)
    if (translated !== key) return translated
  }
  return elig.reason_detail || t('checkin.eligibility.ineligibleDefault')
})

const ctaDisabled = computed(() => {
  if (!status.value || checkingIn.value) return true
  if (!status.value.enabled) return true
  if (status.value.checked_in_today) return true
  if (!status.value.eligibility.eligible) return true
  return false
})

const ctaLabel = computed(() => {
  if (checkingIn.value) return t('checkin.card.checkingIn')
  if (status.value?.checked_in_today) return t('checkin.card.checkedInToday')
  return t('checkin.card.checkInNow')
})

async function loadStatus() {
  loading.value = true
  try {
    status.value = await checkinAPI.getStatus()
  } catch (error) {
    console.warn('Failed to load check-in status:', error)
    status.value = null
  } finally {
    loading.value = false
  }
}

function handleCheckinClick() {
  if (!status.value || ctaDisabled.value) return
  if (status.value.captcha_required) {
    showCaptchaModal.value = true
    return
  }
  void performCheckin()
}

async function handleCaptchaSubmit(proof: ActionCaptchaRequestProof) {
  await performCheckin(proof)
}

async function performCheckin(proof?: ActionCaptchaRequestProof) {
  checkingIn.value = true
  try {
    const res = await checkinAPI.checkin(proof)
    // 成功的第一件事就是关弹窗：后续提示/回调/刷新即使抛错也不能让弹窗卡住。
    closeCaptchaDialog()
    appStore.showSuccess(
      t('checkin.card.checkinSuccess', {
        amount: formatAmount(res.record.total_amount),
        expires: formatDateTimeToMinute(res.record.expires_at)
      })
    )
    emit('checkedIn', res)
    await loadStatus()
  } catch (error) {
    // 请求失败并不代表服务端没有落库：超时、网络抖动、重复点击、
    // 或验证码 token 已被首次尝试消费时，签到可能已经成功。
    // 因此先重新拉取状态；若服务端已记录今日签到，按成功处理并关闭弹窗，
    // 否则保留弹窗让用户重试。
    const recovered = await recoverIfAlreadyCheckedIn()
    if (!recovered) {
      const msg = extractI18nErrorMessage(error, t, 'checkin.errors', t('checkin.errors.default'))
      appStore.showError(msg)
    }
  } finally {
    resetCaptchaDialog()
    checkingIn.value = false
  }
}

// recoverIfAlreadyCheckedIn 重新拉取签到状态；若服务端已记录今日签到，
// 则关闭弹窗、提示用户并通知父组件刷新，避免页面停留在“未签到”的旧状态。
// 返回 true 表示已按成功路径处理。
async function recoverIfAlreadyCheckedIn(): Promise<boolean> {
  let latest: CheckinStatus | null = null
  try {
    latest = await checkinAPI.getStatus()
  } catch (error) {
    console.warn('Failed to resync check-in status:', error)
    return false
  }
  status.value = latest
  if (!latest?.checked_in_today) {
    return false
  }

  closeCaptchaDialog()
  const amount = latest.today_record?.total_amount
  if (typeof amount === 'number') {
    appStore.showSuccess(
      t('checkin.card.checkinSuccess', {
        amount: formatAmount(amount),
        expires: formatDateTimeToMinute(latest.today_record?.expires_at)
      })
    )
  } else {
    appStore.showSuccess(t('checkin.card.checkedInToday'))
  }
  emit('checkedIn')
  return true
}

// closeCaptchaDialog 关闭验证码弹窗。父组件的 show 是唯一真相，
// 同时兜底重置子组件内部 token，避免下一次打开时残留旧凭据。
function closeCaptchaDialog() {
  showCaptchaModal.value = false
  resetCaptchaDialog()
}

// resetCaptchaDialog 重置验证码控件；SDK 销毁失败不得阻断调用方流程。
function resetCaptchaDialog() {
  try {
    captchaDialogRef.value?.reset()
  } catch (error) {
    console.warn('Failed to reset check-in captcha dialog:', error)
  }
}

onMounted(() => {
  void loadStatus()
})
</script>
