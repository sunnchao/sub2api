<template>
  <div class="card">
    <div
      class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 px-6 py-4 dark:border-dark-700"
    >
      <div>
        <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
          {{ t('admin.settings.features.checkin.title') }}
        </h2>
        <p class="mt-0.5 text-sm text-gray-500 dark:text-gray-400">
          {{ t('admin.settings.features.checkin.description') }}
        </p>
      </div>
      <router-link
        to="/admin/checkin"
        class="text-sm font-medium text-primary-600 hover:underline dark:text-primary-400"
      >
        {{ t('admin.settings.features.checkin.manageLink') }} →
      </router-link>
    </div>

    <div v-if="loading" class="flex justify-center py-8">
      <LoadingSpinner size="md" />
    </div>

    <div v-else class="space-y-6 p-6">
      <!-- Master Switch -->
      <div class="flex items-center justify-between gap-4">
        <div>
          <label class="font-medium text-gray-900 dark:text-white">
            {{ t('admin.settings.features.checkin.enabled') }}
          </label>
          <p class="text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.settings.features.checkin.enabledHint') }}
          </p>
        </div>
        <Toggle v-model="form.enabled" data-testid="checkin-enabled-toggle" />
      </div>

      <!-- Reward Config -->
      <div class="grid gap-4 border-t border-gray-100 pt-5 dark:border-dark-700 md:grid-cols-2 lg:grid-cols-4">
        <div>
          <label class="input-label">{{ t('admin.settings.features.checkin.rewardMode') }}</label>
          <Select
            v-model="form.reward_mode"
            :options="rewardModeOptions"
          />
        </div>

        <div v-if="form.reward_mode === 'fixed'">
          <label class="input-label">{{ t('admin.settings.features.checkin.baseAmount') }}</label>
          <input
            v-model.number="form.base_amount"
            type="number"
            min="0"
            step="0.01"
            class="input"
          />
        </div>

        <template v-else>
          <div>
            <label class="input-label">{{ t('admin.settings.features.checkin.minAmount') }}</label>
            <input
              v-model.number="form.min_amount"
              type="number"
              min="0"
              step="0.01"
              class="input"
            />
          </div>
          <div>
            <label class="input-label">{{ t('admin.settings.features.checkin.maxAmount') }}</label>
            <input
              v-model.number="form.max_amount"
              type="number"
              min="0"
              step="0.01"
              class="input"
            />
          </div>
          <div>
            <label class="input-label">{{ t('admin.settings.features.checkin.amountPrecision') }}</label>
            <input
              v-model.number="form.amount_precision"
              type="number"
              min="2"
              max="4"
              step="1"
              class="input"
            />
          </div>
        </template>

        <div>
          <label class="input-label">{{ t('admin.settings.features.checkin.validityDays') }}</label>
          <input
            v-model.number="form.validity_days"
            type="number"
            min="1"
            max="365"
            step="1"
            class="input"
          />
          <p class="input-hint">{{ t('admin.settings.features.checkin.validityDaysHint') }}</p>
        </div>
      </div>

      <!-- Streak Milestones -->
      <div class="space-y-4 border-t border-gray-100 pt-5 dark:border-dark-700">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
              {{ t('admin.settings.features.checkin.milestonesTitle') }}
            </h3>
            <p class="text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.settings.features.checkin.milestonesHint') }}
            </p>
          </div>
          <button
            type="button"
            data-testid="add-milestone-btn"
            class="btn btn-secondary btn-sm"
            @click="addMilestone"
          >
            <Icon name="plus" size="xs" class="mr-1" />
            {{ t('admin.settings.features.checkin.addMilestone') }}
          </button>
        </div>

        <div v-if="form.milestones.length > 0" class="space-y-2.5">
          <div
            v-for="(m, idx) in form.milestones"
            :key="idx"
            class="flex flex-wrap items-end gap-3 rounded-xl border border-gray-200 bg-gray-50/70 p-3 dark:border-dark-600 dark:bg-dark-800/50"
          >
            <div class="w-32">
              <label class="input-label">{{ t('admin.settings.features.checkin.milestoneDays') }}</label>
              <input
                v-model.number="m.days"
                type="number"
                min="2"
                step="1"
                class="input"
              />
            </div>
            <div class="w-36">
              <label class="input-label">{{ t('admin.settings.features.checkin.milestoneBonus') }}</label>
              <input
                v-model.number="m.bonus_amount"
                type="number"
                min="0.01"
                step="0.01"
                class="input"
              />
            </div>
            <button
              type="button"
              class="btn btn-ghost btn-sm text-red-600 hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-900/20"
              @click="removeMilestone(idx)"
            >
              <Icon name="trash" size="sm" class="mr-1" />
              {{ t('admin.settings.features.checkin.removeMilestone') }}
            </button>
          </div>
        </div>

        <div class="flex items-center justify-between gap-4 pt-1">
          <div>
            <label class="text-sm font-medium text-gray-900 dark:text-white">
              {{ t('admin.settings.features.checkin.milestoneCycleRepeat') }}
            </label>
            <p class="text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.settings.features.checkin.milestoneCycleRepeatHint') }}
            </p>
          </div>
          <Toggle v-model="form.milestone_cycle_repeat" />
        </div>
      </div>

      <!-- Eligibility Gates (Collapsible) -->
      <div class="border-t border-gray-100 pt-5 dark:border-dark-700">
        <button
          type="button"
          class="flex w-full items-center justify-between text-left"
          @click="showEligibility = !showEligibility"
        >
          <div>
            <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
              {{ t('admin.settings.features.checkin.eligibilityTitle') }}
            </h3>
            <p class="text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.settings.features.checkin.eligibilityHint') }}
            </p>
          </div>
          <Icon
            :name="showEligibility ? 'chevronDown' : 'chevronRight'"
            size="sm"
            class="text-gray-400"
          />
        </button>

        <div v-if="showEligibility" class="mt-4 space-y-4">
          <div class="grid gap-4 md:grid-cols-3">
            <div>
              <label class="input-label">{{ t('admin.settings.features.checkin.minAccountAgeDays') }}</label>
              <input
                v-model.number="form.eligibility.min_account_age_days"
                type="number"
                min="0"
                step="1"
                class="input"
              />
            </div>
            <div>
              <label class="input-label">{{ t('admin.settings.features.checkin.minTotalRecharged') }}</label>
              <input
                v-model.number="form.eligibility.min_total_recharged"
                type="number"
                min="0"
                step="0.01"
                class="input"
              />
            </div>
            <div>
              <label class="input-label">{{ t('admin.settings.features.checkin.maxAvailableBalance') }}</label>
              <input
                v-model.number="form.eligibility.max_available_balance"
                type="number"
                min="0"
                step="0.01"
                class="input"
              />
            </div>
          </div>

          <div>
            <label class="input-label">{{ t('admin.settings.features.checkin.allowedSignupSources') }}</label>
            <p class="mb-2 text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.settings.features.checkin.allowedSignupSourcesHint') }}
            </p>
            <div class="flex flex-wrap gap-2">
              <button
                v-for="source in SIGNUP_SOURCES"
                :key="source"
                type="button"
                :class="[
                  'rounded-lg border px-3 py-1.5 text-xs font-medium transition-colors',
                  form.eligibility.allowed_signup_sources.includes(source)
                    ? 'border-primary-500 bg-primary-50 text-primary-700 dark:border-primary-400 dark:bg-primary-900/30 dark:text-primary-300'
                    : 'border-gray-200 bg-white text-gray-600 hover:bg-gray-50 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-300'
                ]"
                @click="toggleSignupSource(source)"
              >
                {{ source }}
              </button>
            </div>
          </div>
        </div>
      </div>

      <!-- Anti-Abuse & Budget -->
      <div class="space-y-4 border-t border-gray-100 pt-5 dark:border-dark-700">
        <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
          {{ t('admin.settings.features.checkin.riskTitle') }}
        </h3>

        <div class="flex items-center justify-between gap-4">
          <div>
            <label class="text-sm font-medium text-gray-900 dark:text-white">
              {{ t('admin.settings.features.checkin.captchaEnabled') }}
            </label>
            <p class="text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.settings.features.checkin.captchaEnabledHint') }}
            </p>
          </div>
          <Toggle v-model="form.risk.captcha_enabled" data-testid="checkin-captcha-toggle" />
        </div>

        <div
          v-if="showCaptchaMissingWarning"
          data-testid="checkin-captcha-warning"
          class="flex items-start gap-2 rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 text-xs text-amber-800 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-200"
        >
          <Icon name="exclamationTriangle" size="sm" class="mt-0.5 flex-shrink-0 text-amber-500" />
          <span>{{ t('admin.settings.features.checkin.captchaNotConfiguredWarning') }}</span>
        </div>

        <div
          v-if="serverWarnings.length > 0"
          data-testid="checkin-server-warnings"
          class="space-y-1 rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 text-xs text-amber-800 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-200"
        >
          <div v-for="(warn, idx) in serverWarnings" :key="idx">{{ warn }}</div>
        </div>

        <div class="grid gap-4 md:grid-cols-2">
          <div>
            <label class="input-label">{{ t('admin.settings.features.checkin.maxAccountsPerIpDay') }}</label>
            <input
              v-model.number="form.risk.max_accounts_per_ip_day"
              type="number"
              min="0"
              step="1"
              class="input"
            />
          </div>
          <div>
            <label class="input-label">{{ t('admin.settings.features.checkin.dailyTotalBudgetUsd') }}</label>
            <input
              v-model.number="form.risk.daily_total_budget_usd"
              type="number"
              min="0"
              step="0.01"
              class="input"
            />
          </div>
        </div>
      </div>

      <!-- Rules Description -->
      <div class="border-t border-gray-100 pt-5 dark:border-dark-700">
        <label class="input-label">{{ t('admin.settings.features.checkin.rulesDescription') }}</label>
        <textarea
          v-model="form.rules_description"
          rows="4"
          class="input"
          :placeholder="t('admin.settings.features.checkin.rulesDescriptionPlaceholder')"
        />
      </div>

      <!-- Validation Error -->
      <p v-if="validationError" class="text-xs text-red-600 dark:text-red-400">
        {{ validationError }}
      </p>

      <!-- Standalone Save Footer -->
      <div class="flex justify-end border-t border-gray-100 pt-4 dark:border-dark-700">
        <button
          type="button"
          data-testid="checkin-save-btn"
          class="btn btn-primary btn-sm"
          :disabled="saving || loading"
          @click="saveConfig"
        >
          {{ saving ? t('common.saving') : t('admin.settings.features.checkin.save') }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Toggle from '@/components/common/Toggle.vue'
import Select from '@/components/common/Select.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import Icon from '@/components/icons/Icon.vue'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { CheckinSettings } from '@/types'

const SIGNUP_SOURCES = ['email', 'linuxdo', 'oidc', 'wechat', 'github', 'google', 'dingtalk'] as const

const props = defineProps<{
  captchaConfigured?: boolean
}>()

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(false)
const saving = ref(false)
const showEligibility = ref(false)
const validationError = ref('')
const serverWarnings = ref<string[]>([])

const form = reactive<CheckinSettings>({
  enabled: false,
  reward_mode: 'fixed',
  base_amount: 0.1,
  min_amount: 0.05,
  max_amount: 0.2,
  amount_precision: 2,
  validity_days: 1,
  milestones: [],
  milestone_cycle_repeat: false,
  eligibility: {
    min_account_age_days: 0,
    min_total_recharged: 0,
    max_available_balance: 0,
    allowed_signup_sources: []
  },
  risk: {
    captcha_enabled: false,
    max_accounts_per_ip_day: 0,
    daily_total_budget_usd: 0
  },
  rules_description: ''
})

const rewardModeOptions = computed(() => [
  { value: 'fixed', label: t('admin.settings.features.checkin.rewardModeFixed') },
  { value: 'random', label: t('admin.settings.features.checkin.rewardModeRandom') }
])

const hasCaptchaProvider = computed(() => {
  if (props.captchaConfigured !== undefined) {
    return props.captchaConfigured
  }
  const s = appStore.cachedPublicSettings
  return Boolean(s?.turnstile_enabled || s?.tencent_captcha_enabled || s?.aliyun_captcha_enabled)
})

const showCaptchaMissingWarning = computed(
  () => form.risk.captcha_enabled && !hasCaptchaProvider.value
)

function applyConfig(cfg: Partial<CheckinSettings>) {
  form.enabled = Boolean(cfg.enabled)
  form.reward_mode = cfg.reward_mode === 'random' ? 'random' : 'fixed'
  form.base_amount = Number(cfg.base_amount ?? 0.1)
  form.min_amount = Number(cfg.min_amount ?? 0.05)
  form.max_amount = Number(cfg.max_amount ?? 0.2)
  form.amount_precision = Number(cfg.amount_precision ?? 2)
  form.validity_days = Number(cfg.validity_days ?? 1)
  form.milestones = Array.isArray(cfg.milestones)
    ? cfg.milestones.map((m) => ({ days: Number(m.days), bonus_amount: Number(m.bonus_amount) }))
    : []
  form.milestone_cycle_repeat = Boolean(cfg.milestone_cycle_repeat)
  form.eligibility = {
    min_account_age_days: Number(cfg.eligibility?.min_account_age_days ?? 0),
    min_total_recharged: Number(cfg.eligibility?.min_total_recharged ?? 0),
    max_available_balance: Number(cfg.eligibility?.max_available_balance ?? 0),
    allowed_signup_sources: Array.isArray(cfg.eligibility?.allowed_signup_sources)
      ? [...cfg.eligibility.allowed_signup_sources]
      : []
  }
  form.risk = {
    captcha_enabled: Boolean(cfg.risk?.captcha_enabled),
    max_accounts_per_ip_day: Number(cfg.risk?.max_accounts_per_ip_day ?? 0),
    daily_total_budget_usd: Number(cfg.risk?.daily_total_budget_usd ?? 0)
  }
  form.rules_description = cfg.rules_description ?? ''
  serverWarnings.value = Array.isArray(cfg.warnings) ? cfg.warnings : []
}

function addMilestone() {
  const maxDays = form.milestones.reduce((acc, m) => Math.max(acc, Number(m.days) || 0), 1)
  form.milestones.push({
    days: maxDays < 3 ? 3 : maxDays + 1,
    bonus_amount: 0.5
  })
}

function removeMilestone(idx: number) {
  form.milestones.splice(idx, 1)
}

function toggleSignupSource(source: string) {
  const idx = form.eligibility.allowed_signup_sources.indexOf(source)
  if (idx >= 0) {
    form.eligibility.allowed_signup_sources.splice(idx, 1)
  } else {
    form.eligibility.allowed_signup_sources.push(source)
  }
}

function validateConfig(): string | null {
  if (!Number.isInteger(form.validity_days) || form.validity_days < 1 || form.validity_days > 365) {
    return t('admin.settings.features.checkin.invalidValidityDays')
  }
  if (form.reward_mode === 'fixed') {
    if (!Number.isFinite(form.base_amount) || form.base_amount <= 0) {
      return t('admin.settings.features.checkin.invalidBaseAmount')
    }
  } else {
    if (
      !Number.isFinite(form.min_amount) ||
      !Number.isFinite(form.max_amount) ||
      form.min_amount <= 0 ||
      form.max_amount <= 0 ||
      form.min_amount > form.max_amount
    ) {
      return t('admin.settings.features.checkin.invalidRandomRange')
    }
    if (!Number.isInteger(form.amount_precision) || form.amount_precision < 2 || form.amount_precision > 4) {
      return t('admin.settings.features.checkin.invalidPrecision')
    }
  }
  const seenDays = new Set<number>()
  for (const m of form.milestones) {
    if (!Number.isInteger(m.days) || m.days <= 1) {
      return t('admin.settings.features.checkin.invalidMilestoneDays')
    }
    if (seenDays.has(m.days)) {
      return t('admin.settings.features.checkin.duplicateMilestoneDays')
    }
    seenDays.add(m.days)
    if (!Number.isFinite(m.bonus_amount) || m.bonus_amount <= 0) {
      return t('admin.settings.features.checkin.invalidMilestoneBonus')
    }
  }
  return null
}

async function loadConfig() {
  loading.value = true
  try {
    const res = await adminAPI?.checkin?.getConfig?.()
    if (res) {
      applyConfig(res)
    }
  } catch (error) {
    console.warn('Failed to load check-in config:', error)
  } finally {
    loading.value = false
  }
}

async function saveConfig() {
  validationError.value = ''
  const errMsg = validateConfig()
  if (errMsg) {
    validationError.value = errMsg
    return
  }

  saving.value = true
  try {
    const payload: CheckinSettings = {
      enabled: form.enabled,
      reward_mode: form.reward_mode,
      base_amount: Number(form.base_amount),
      min_amount: Number(form.min_amount),
      max_amount: Number(form.max_amount),
      amount_precision: Number(form.amount_precision),
      validity_days: Number(form.validity_days),
      milestones: [...form.milestones]
        .map((m) => ({ days: Number(m.days), bonus_amount: Number(m.bonus_amount) }))
        .sort((a, b) => a.days - b.days),
      milestone_cycle_repeat: form.milestone_cycle_repeat,
      eligibility: {
        min_account_age_days: Math.max(0, Number(form.eligibility.min_account_age_days) || 0),
        min_total_recharged: Math.max(0, Number(form.eligibility.min_total_recharged) || 0),
        max_available_balance: Math.max(0, Number(form.eligibility.max_available_balance) || 0),
        allowed_signup_sources: [...form.eligibility.allowed_signup_sources]
      },
      risk: {
        captcha_enabled: form.risk.captcha_enabled,
        max_accounts_per_ip_day: Math.max(0, Number(form.risk.max_accounts_per_ip_day) || 0),
        daily_total_budget_usd: Math.max(0, Number(form.risk.daily_total_budget_usd) || 0)
      },
      rules_description: form.rules_description
    }

    const res = await adminAPI.checkin.updateConfig(payload)
    applyConfig(res)
    try {
      await appStore.fetchPublicSettings(true)
    } catch (refreshError) {
      console.warn('Failed to refresh public settings after check-in config save:', refreshError)
    }
    if (res.warnings && res.warnings.length > 0) {
      appStore.showWarning(res.warnings[0])
    } else {
      appStore.showSuccess(t('admin.settings.features.checkin.saved'))
    }
  } catch (error) {
    appStore.showError(
      extractApiErrorMessage(error, t('admin.settings.features.checkin.saveFailed'))
    )
  } finally {
    saving.value = false
  }
}

onMounted(() => {
  void loadConfig()
})
</script>
