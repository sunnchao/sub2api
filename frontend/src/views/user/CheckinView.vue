<template>
  <AppLayout>
    <div class="space-y-6">
      <div v-if="loadingStatus" class="flex items-center justify-center py-12">
        <LoadingSpinner size="lg" />
      </div>

      <template v-else-if="status">
        <!-- Hero Card -->
        <div class="card overflow-hidden">
          <div class="bg-gradient-to-br from-primary-600 via-primary-600 to-indigo-700 px-6 py-7 text-white">
            <div class="flex flex-col gap-6 lg:flex-row lg:items-center lg:justify-between">
              <div class="space-y-3">
                <div class="flex flex-wrap items-center gap-2 text-xs text-primary-100">
                  <span class="inline-flex items-center gap-1 rounded-full bg-white/15 px-2.5 py-0.5 font-medium">
                    <Icon name="calendar" size="xs" />
                    {{ status.server_date }} ({{ status.timezone }})
                  </span>
                  <span class="inline-flex items-center gap-1 rounded-full bg-amber-400/20 px-2.5 py-0.5 font-medium text-amber-100">
                    <Icon name="fire" size="xs" />
                    {{ t('checkin.card.streakDays', { days: status.current_streak }) }}
                  </span>
                </div>

                <h1 class="text-2xl font-bold tracking-tight">
                  {{ status.checked_in_today ? t('checkin.page.heroCheckedTitle') : t('checkin.page.heroTitle') }}
                </h1>
                <p class="text-sm text-primary-100">
                  <template v-if="status.checked_in_today && status.today_record">
                    {{
                      t('checkin.page.heroCheckedSubtitle', {
                        amount: formatAmount(status.today_record.total_amount),
                        expires: formatDateTimeToMinute(status.today_record.expires_at)
                      })
                    }}
                  </template>
                  <template v-else>
                    {{
                      t('checkin.page.heroSubtitle', {
                        reward: rewardPreviewText,
                        days: status.reward_preview.validity_days
                      })
                    }}
                  </template>
                </p>

                <div
                  v-if="!status.checked_in_today && !status.eligibility.eligible"
                  data-testid="checkin-page-ineligible"
                  class="inline-flex items-center gap-1.5 rounded-lg bg-amber-500/20 px-3 py-1.5 text-xs font-medium text-amber-100"
                >
                  <Icon name="exclamationTriangle" size="sm" />
                  {{ eligibilityMessage }}
                </div>
              </div>

              <div class="flex flex-col items-stretch gap-4 sm:flex-row sm:items-center">
                <div class="rounded-xl bg-white/10 px-4 py-3 backdrop-blur-sm">
                  <p class="text-xs text-primary-100">{{ t('checkin.page.activeExpiring') }}</p>
                  <p class="mt-0.5 text-2xl font-bold">
                    ${{ formatAmount(status.expiring_summary.total_active) }}
                  </p>
                  <p v-if="status.expiring_summary.frozen_amount > 0" class="mt-0.5 text-xs text-amber-200">
                    {{ t('common.frozenBalance') }}: ${{ formatAmount(status.expiring_summary.frozen_amount) }}
                  </p>
                  <p v-if="status.expiring_summary.nearest_expires_at" class="mt-1 text-xs text-primary-100">
                    {{ t('common.nearestExpiring') }}:
                    {{ formatDateTimeToMinute(status.expiring_summary.nearest_expires_at) }}
                    <span v-if="nearestCountdown" class="ml-1 rounded bg-white/15 px-1.5 py-0.5 font-mono text-[11px]">
                      {{ nearestCountdown }}
                    </span>
                  </p>
                </div>

                <button
                  type="button"
                  data-testid="checkin-submit-btn"
                  class="btn bg-white px-6 py-3 text-sm font-semibold text-primary-700 shadow-md hover:bg-primary-50 disabled:cursor-not-allowed disabled:opacity-60"
                  :disabled="ctaDisabled"
                  @click="handleCheckinClick"
                >
                  <Icon name="sparkles" size="sm" class="mr-1.5" />
                  {{ ctaLabel }}
                </button>
              </div>
            </div>
          </div>
        </div>

        <!-- Milestone Progress Card -->
        <div v-if="sortedMilestones.length > 0" class="card p-6">
          <div class="mb-4 flex flex-wrap items-center justify-between gap-2">
            <div>
              <h2 class="text-base font-semibold text-gray-900 dark:text-white">
                {{ t('checkin.page.milestonesTitle') }}
              </h2>
              <p class="text-xs text-gray-500 dark:text-gray-400">
                {{
                  status.milestone_cycle_repeat
                    ? t('checkin.page.milestoneRepeatOn', { day: status.milestone_cycle_day })
                    : t('checkin.page.milestoneRepeatOff', { day: status.milestone_cycle_day })
                }}
              </p>
            </div>
            <span v-if="status.next_milestone" data-testid="checkin-next-milestone-badge" class="badge badge-primary">
              {{
                t('checkin.card.nextMilestoneIn', {
                  days: nextMilestoneDaysLeft,
                  bonus: formatAmount(status.next_milestone.bonus_amount)
                })
              }}
            </span>
          </div>

          <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            <div
              v-for="m in sortedMilestones"
              :key="m.days"
              :class="[
                'rounded-xl border p-4 transition-colors',
                milestoneState(m) === 'achieved'
                  ? 'border-emerald-200 bg-emerald-50/70 dark:border-emerald-500/30 dark:bg-emerald-900/20'
                  : milestoneState(m) === 'next'
                    ? 'border-primary-300 bg-primary-50/60 dark:border-primary-500/40 dark:bg-primary-900/20'
                    : 'border-gray-200 bg-gray-50/60 dark:border-dark-700 dark:bg-dark-800/60'
              ]"
            >
              <div class="flex items-center justify-between">
                <span class="text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('checkin.page.milestoneDayLabel', { days: m.days }) }}
                </span>
                <Icon
                  :name="milestoneState(m) === 'achieved' ? 'checkCircle' : 'trophy'"
                  size="sm"
                  :class="
                    milestoneState(m) === 'achieved'
                      ? 'text-emerald-500'
                      : milestoneState(m) === 'next'
                        ? 'text-primary-500'
                        : 'text-gray-400'
                  "
                />
              </div>
              <p class="mt-1.5 text-lg font-bold text-gray-900 dark:text-white">
                +${{ formatAmount(m.bonus_amount) }}
              </p>
              <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                {{
                  milestoneState(m) === 'achieved'
                    ? t('checkin.page.milestoneAchieved')
                    : milestoneState(m) === 'next'
                      ? t('checkin.page.milestoneNext')
                      : t('checkin.page.milestoneLocked')
                }}
              </p>
            </div>
          </div>
        </div>

        <!-- Monthly Calendar Card -->
        <div class="card p-6">
          <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
            <div>
              <h2 class="text-base font-semibold text-gray-900 dark:text-white">
                {{ t('checkin.page.calendarTitle') }}
              </h2>
              <p v-if="calendar" class="text-xs text-gray-500 dark:text-gray-400">
                {{
                  t('checkin.page.calendarSummary', {
                    days: calendar.month_checkin_days,
                    amount: formatAmount(calendar.month_total_amount)
                  })
                }}
              </p>
            </div>

            <div class="flex items-center gap-2">
              <button
                type="button"
                data-testid="calendar-prev-month"
                class="btn btn-secondary btn-sm"
                @click="shiftMonth(-1)"
              >
                <Icon name="chevronLeft" size="sm" />
              </button>
              <span class="min-w-[88px] text-center font-mono text-sm font-semibold text-gray-900 dark:text-white">
                {{ currentMonth }}
              </span>
              <button
                type="button"
                data-testid="calendar-next-month"
                class="btn btn-secondary btn-sm"
                :disabled="isCurrentServerMonth"
                @click="shiftMonth(1)"
              >
                <Icon name="chevronRight" size="sm" />
              </button>
            </div>
          </div>

          <div v-if="loadingCalendar" class="flex justify-center py-8">
            <LoadingSpinner size="md" />
          </div>

          <div v-else>
            <div class="grid grid-cols-7 gap-1.5 text-center text-xs font-medium text-gray-500 dark:text-gray-400">
              <div v-for="wd in weekdayLabels" :key="wd" class="py-1.5">
                {{ wd }}
              </div>
            </div>
            <div class="mt-1 grid grid-cols-7 gap-1.5">
              <div
                v-for="(cell, idx) in calendarCells"
                :key="idx"
                :class="[
                  'min-h-[64px] rounded-lg border p-1.5 text-xs transition-colors',
                  !cell.day
                    ? 'border-transparent bg-transparent'
                    : cell.record
                      ? 'border-emerald-200 bg-emerald-50 dark:border-emerald-500/30 dark:bg-emerald-900/20'
                      : cell.isToday
                        ? 'border-primary-400 bg-primary-50/40 dark:border-primary-500/50 dark:bg-primary-900/10'
                        : 'border-gray-100 bg-gray-50/50 dark:border-dark-700 dark:bg-dark-800/40'
                ]"
              >
                <template v-if="cell.day">
                  <div class="flex items-center justify-between">
                    <span
                      :class="[
                        'font-mono font-medium',
                        cell.isToday
                          ? 'text-primary-600 dark:text-primary-400'
                          : 'text-gray-700 dark:text-gray-300'
                      ]"
                    >
                      {{ cell.day }}
                    </span>
                    <Icon
                      v-if="cell.record?.milestone_hit"
                      name="trophy"
                      size="xs"
                      class="text-amber-500"
                    />
                  </div>
                  <div v-if="cell.record" class="mt-1.5 font-mono text-[11px] font-semibold text-emerald-700 dark:text-emerald-300">
                    +${{ formatAmount(cell.record.total_amount) }}
                  </div>
                </template>
              </div>
            </div>
          </div>
        </div>

        <!-- Expiring Lots & Check-in History Tabs -->
        <div class="card overflow-hidden">
          <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 px-6 py-4 dark:border-dark-700">
            <div class="flex items-center gap-2">
              <button
                type="button"
                data-testid="tab-lots"
                :class="[
                  'rounded-lg px-3 py-1.5 text-sm font-medium transition-colors',
                  activeTab === 'lots'
                    ? 'bg-primary-600 text-white'
                    : 'text-gray-600 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-dark-700'
                ]"
                @click="switchTab('lots')"
              >
                {{ t('checkin.page.tabs.lots') }}
              </button>
              <button
                type="button"
                data-testid="tab-history"
                :class="[
                  'rounded-lg px-3 py-1.5 text-sm font-medium transition-colors',
                  activeTab === 'records'
                    ? 'bg-primary-600 text-white'
                    : 'text-gray-600 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-dark-700'
                ]"
                @click="switchTab('records')"
              >
                {{ t('checkin.page.tabs.history') }}
              </button>
            </div>

            <div v-if="activeTab === 'lots'" class="flex flex-wrap items-center gap-1.5">
              <button
                v-for="opt in lotStatusOptions"
                :key="opt.value"
                type="button"
                :class="[
                  'rounded-md px-2.5 py-1 text-xs font-medium transition-colors',
                  lotStatusFilter === opt.value
                    ? 'bg-gray-900 text-white dark:bg-white dark:text-gray-900'
                    : 'bg-gray-100 text-gray-600 hover:bg-gray-200 dark:bg-dark-700 dark:text-gray-300'
                ]"
                @click="selectLotStatus(opt.value)"
              >
                {{ opt.label }}
              </button>
            </div>
          </div>

          <!-- Lots Tab -->
          <div v-if="activeTab === 'lots'">
            <div v-if="loadingLots" class="flex justify-center py-10">
              <LoadingSpinner size="md" />
            </div>
            <EmptyState
              v-else-if="lots.length === 0"
              icon="clock"
              :title="t('checkin.page.emptyLots')"
            />
            <template v-else>
              <div class="table-container">
                <table class="table">
                  <thead>
                    <tr>
                      <th>{{ t('checkin.page.lotsColumns.createdAt') }}</th>
                      <th>{{ t('checkin.page.lotsColumns.initial') }}</th>
                      <th>{{ t('checkin.page.lotsColumns.remaining') }}</th>
                      <th>{{ t('checkin.page.lotsColumns.frozen') }}</th>
                      <th>{{ t('checkin.page.lotsColumns.consumed') }}</th>
                      <th>{{ t('checkin.page.lotsColumns.expired') }}</th>
                      <th>{{ t('checkin.page.lotsColumns.expiresAt') }}</th>
                      <th>{{ t('checkin.page.lotsColumns.status') }}</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr v-for="lot in lots" :key="lot.id">
                      <td class="whitespace-nowrap text-xs text-gray-600 dark:text-gray-300">
                        {{ formatDateTime(lot.created_at) }}
                      </td>
                      <td class="font-mono text-sm font-medium text-gray-900 dark:text-white">
                        ${{ formatAmount(lot.initial_amount) }}
                      </td>
                      <td class="font-mono text-sm font-semibold text-emerald-600 dark:text-emerald-400">
                        ${{ formatAmount(lot.remaining_amount) }}
                      </td>
                      <td class="font-mono text-sm text-amber-600 dark:text-amber-400">
                        ${{ formatAmount(lot.frozen_amount) }}
                      </td>
                      <td class="font-mono text-sm text-gray-700 dark:text-gray-300">
                        ${{ formatAmount(lot.consumed_amount) }}
                      </td>
                      <td class="font-mono text-sm text-gray-500 dark:text-gray-400">
                        ${{ formatAmount(lot.expired_amount) }}
                      </td>
                      <td class="whitespace-nowrap text-xs text-gray-600 dark:text-gray-300">
                        {{ formatDateTimeToMinute(lot.expires_at) }}
                      </td>
                      <td>
                        <StatusBadge
                          :status="lotBadgeStatus(lot.status)"
                          :label="t(`checkin.page.lotStatus.${lot.status}`)"
                        />
                      </td>
                    </tr>
                  </tbody>
                </table>
              </div>
              <Pagination
                v-if="lotsTotal > 0"
                :total="lotsTotal"
                :page="lotsPage"
                :page-size="lotsPageSize"
                @update:page="loadLots"
                @update:page-size="onLotsPageSizeChange"
              />
            </template>
          </div>

          <!-- Records Tab -->
          <div v-else>
            <div v-if="loadingRecords" class="flex justify-center py-10">
              <LoadingSpinner size="md" />
            </div>
            <EmptyState
              v-else-if="records.length === 0"
              icon="calendar"
              :title="t('checkin.page.emptyRecords')"
            />
            <template v-else>
              <div class="table-container">
                <table class="table">
                  <thead>
                    <tr>
                      <th>{{ t('checkin.page.historyColumns.date') }}</th>
                      <th>{{ t('checkin.page.historyColumns.streak') }}</th>
                      <th>{{ t('checkin.page.historyColumns.baseAmount') }}</th>
                      <th>{{ t('checkin.page.historyColumns.bonusAmount') }}</th>
                      <th>{{ t('checkin.page.historyColumns.totalAmount') }}</th>
                      <th>{{ t('checkin.page.historyColumns.expiresAt') }}</th>
                      <th>{{ t('checkin.page.historyColumns.createdAt') }}</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr v-for="rec in records" :key="rec.id">
                      <td class="font-mono text-sm font-medium text-gray-900 dark:text-white">
                        {{ rec.checkin_date }}
                      </td>
                      <td class="text-sm text-gray-700 dark:text-gray-300">
                        {{ t('checkin.card.streakDays', { days: rec.streak_days }) }}
                      </td>
                      <td class="font-mono text-sm text-gray-700 dark:text-gray-300">
                        ${{ formatAmount(rec.base_amount) }}
                      </td>
                      <td class="font-mono text-sm text-amber-600 dark:text-amber-400">
                        +${{ formatAmount(rec.milestone_bonus_amount) }}
                      </td>
                      <td class="font-mono text-sm font-semibold text-emerald-600 dark:text-emerald-400">
                        +${{ formatAmount(rec.total_amount) }}
                      </td>
                      <td class="whitespace-nowrap text-xs text-gray-600 dark:text-gray-300">
                        {{ formatDateTimeToMinute(rec.expires_at) }}
                      </td>
                      <td class="whitespace-nowrap text-xs text-gray-500 dark:text-gray-400">
                        {{ formatDateTime(rec.created_at) }}
                      </td>
                    </tr>
                  </tbody>
                </table>
              </div>
              <Pagination
                v-if="recordsTotal > 0"
                :total="recordsTotal"
                :page="recordsPage"
                :page-size="recordsPageSize"
                @update:page="loadRecords"
                @update:page-size="onRecordsPageSizeChange"
              />
            </template>
          </div>
        </div>

        <!-- Rules Card -->
        <div class="card p-6">
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('checkin.page.rulesTitle') }}
          </h2>
          <div
            v-if="renderedRulesHtml"
            data-testid="checkin-custom-rules"
            class="mt-3 text-sm leading-relaxed text-gray-600 dark:text-gray-300 [&_a]:text-primary-600 [&_a]:underline [&_h1]:text-base [&_h1]:font-semibold [&_h2]:text-sm [&_h2]:font-semibold [&_ol]:my-1 [&_ol]:ml-5 [&_ol]:list-decimal [&_p]:my-1 [&_strong]:font-semibold [&_ul]:my-1 [&_ul]:ml-5 [&_ul]:list-disc"
            v-html="renderedRulesHtml"
          ></div>
          <ul
            v-else
            data-testid="checkin-default-rules"
            class="mt-3 list-disc space-y-1.5 pl-5 text-sm text-gray-600 dark:text-gray-300"
          >
            <li>
              {{
                t('checkin.page.defaultRuleReward', {
                  reward: rewardPreviewText,
                  days: status.reward_preview.validity_days,
                  timezone: status.timezone
                })
              }}
            </li>
            <li>{{ t('checkin.page.defaultRuleFefo') }}</li>
            <li>{{ t('checkin.page.defaultRuleStreak') }}</li>
            <li v-if="sortedMilestones.length > 0">
              {{
                status.milestone_cycle_repeat
                  ? t('checkin.page.defaultRuleCycleRepeat')
                  : t('checkin.page.defaultRuleCycleOnce')
              }}
            </li>
          </ul>
        </div>
      </template>

      <EmptyState
        v-else
        icon="calendar"
        :title="t('checkin.errors.default')"
      />
    </div>

    <CheckinCaptchaDialog
      ref="captchaDialogRef"
      :show="showCaptchaModal"
      :submitting="checkingIn"
      @close="showCaptchaModal = false"
      @submit="handleCaptchaSubmit"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import DOMPurify from 'dompurify'
import { marked } from 'marked'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Pagination from '@/components/common/Pagination.vue'
import StatusBadge from '@/components/common/StatusBadge.vue'
import CheckinCaptchaDialog from '@/components/user/checkin/CheckinCaptchaDialog.vue'
import { checkinAPI } from '@/api/checkin'
import { useAppStore, useAuthStore } from '@/stores'
import { extractI18nErrorMessage } from '@/utils/apiError'
import { formatCountdown, formatDateTime, formatDateTimeToMinute } from '@/utils/format'
import type {
  ActionCaptchaRequestProof,
  CheckinCalendarDay,
  CheckinCalendarResponse,
  CheckinMilestoneRule,
  CheckinRecord,
  CheckinStatus,
  ExpiringBalanceLot,
  ExpiringBalanceLotStatus
} from '@/types'

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()

const loadingStatus = ref(true)
const loadingCalendar = ref(false)
const loadingLots = ref(false)
const loadingRecords = ref(false)
const checkingIn = ref(false)
const showCaptchaModal = ref(false)
const captchaDialogRef = ref<InstanceType<typeof CheckinCaptchaDialog> | null>(null)

const status = ref<CheckinStatus | null>(null)
const calendar = ref<CheckinCalendarResponse | null>(null)
const currentMonth = ref(new Date().toISOString().slice(0, 7))

const activeTab = ref<'lots' | 'records'>('lots')
const lotStatusFilter = ref<'all' | ExpiringBalanceLotStatus>('all')
const lots = ref<ExpiringBalanceLot[]>([])
const lotsTotal = ref(0)
const lotsPage = ref(1)
const lotsPageSize = ref(10)

const records = ref<CheckinRecord[]>([])
const recordsTotal = ref(0)
const recordsPage = ref(1)
const recordsPageSize = ref(10)

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

const nearestCountdown = computed(() =>
  formatCountdown(status.value?.expiring_summary.nearest_expires_at)
)

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

const sortedMilestones = computed<CheckinMilestoneRule[]>(() => {
  const list = status.value?.milestones ?? []
  return [...list].sort((a, b) => a.days - b.days)
})

const isMilestoneCycleWrapped = computed(() => {
  const next = status.value?.next_milestone
  if (!status.value?.milestone_cycle_repeat || !next) return false
  const cycleDay = status.value.milestone_cycle_day ?? 0
  const maxDays =
    sortedMilestones.value.length > 0
      ? sortedMilestones.value[sortedMilestones.value.length - 1].days
      : 0
  return next.days < cycleDay || (maxDays > 0 && cycleDay >= maxDays && next.days <= cycleDay)
})

const nextMilestoneDaysLeft = computed(() => {
  const next = status.value?.next_milestone
  if (!next) return 0
  if (isMilestoneCycleWrapped.value) {
    return next.days
  }
  return Math.max(0, next.days - (status.value?.milestone_cycle_day ?? 0))
})

function milestoneState(rule: CheckinMilestoneRule): 'achieved' | 'next' | 'locked' {
  if (isMilestoneCycleWrapped.value) {
    if (status.value?.next_milestone?.days === rule.days) return 'next'
    return 'locked'
  }
  const cycleDay = status.value?.milestone_cycle_day ?? 0
  if (cycleDay >= rule.days) return 'achieved'
  if (status.value?.next_milestone?.days === rule.days) return 'next'
  return 'locked'
}

const serverMonth = computed(() => status.value?.server_date?.slice(0, 7) || new Date().toISOString().slice(0, 7))
const isCurrentServerMonth = computed(() => currentMonth.value >= serverMonth.value)

const weekdayLabels = computed(() => [
  t('checkin.page.weekdays.sun'),
  t('checkin.page.weekdays.mon'),
  t('checkin.page.weekdays.tue'),
  t('checkin.page.weekdays.wed'),
  t('checkin.page.weekdays.thu'),
  t('checkin.page.weekdays.fri'),
  t('checkin.page.weekdays.sat')
])

interface CalendarCell {
  day: number | null
  dateStr: string
  isToday: boolean
  record?: CheckinCalendarDay
}

const calendarCells = computed<CalendarCell[]>(() => {
  const [yStr, mStr] = currentMonth.value.split('-')
  const year = Number(yStr)
  const month = Number(mStr)
  if (!year || !month) return []

  const firstDayOfWeek = new Date(Date.UTC(year, month - 1, 1)).getUTCDay()
  const daysInMonth = new Date(Date.UTC(year, month, 0)).getUTCDate()
  const recordMap = new Map<string, CheckinCalendarDay>()
  for (const item of calendar.value?.checked_dates ?? []) {
    recordMap.set(item.date, item)
  }

  const cells: CalendarCell[] = []
  for (let i = 0; i < firstDayOfWeek; i++) {
    cells.push({ day: null, dateStr: '', isToday: false })
  }
  for (let d = 1; d <= daysInMonth; d++) {
    const dateStr = `${yStr}-${mStr}-${String(d).padStart(2, '0')}`
    cells.push({
      day: d,
      dateStr,
      isToday: dateStr === status.value?.server_date,
      record: recordMap.get(dateStr)
    })
  }
  return cells
})

const lotStatusOptions = computed<{ value: 'all' | ExpiringBalanceLotStatus; label: string }[]>(() => [
  { value: 'all', label: t('common.all') },
  { value: 'active', label: t('checkin.page.lotStatus.active') },
  { value: 'exhausted', label: t('checkin.page.lotStatus.exhausted') },
  { value: 'expired', label: t('checkin.page.lotStatus.expired') }
])

function lotBadgeStatus(s: ExpiringBalanceLotStatus): string {
  if (s === 'active') return 'active'
  if (s === 'expired') return 'error'
  return 'exhausted'
}

const renderedRulesHtml = computed(() => {
  const raw = (status.value?.rules_description || '').trim()
  if (!raw) return ''
  return DOMPurify.sanitize(marked.parse(raw, { async: false, gfm: true, breaks: true }))
})

async function loadStatus() {
  loadingStatus.value = true
  try {
    const res = await checkinAPI.getStatus()
    status.value = res
    if (res.server_date) {
      currentMonth.value = res.server_date.slice(0, 7)
    }
  } catch (error) {
    console.warn('Failed to load check-in status:', error)
    status.value = null
  } finally {
    loadingStatus.value = false
  }
}

async function loadCalendar(month = currentMonth.value) {
  loadingCalendar.value = true
  try {
    calendar.value = await checkinAPI.getCalendar(month)
  } catch (error) {
    console.warn('Failed to load check-in calendar:', error)
    calendar.value = null
  } finally {
    loadingCalendar.value = false
  }
}

async function loadLots(page = lotsPage.value) {
  lotsPage.value = page
  loadingLots.value = true
  try {
    const res = await checkinAPI.getLots(page, lotsPageSize.value, lotStatusFilter.value)
    lots.value = res.items ?? []
    lotsTotal.value = res.total ?? 0
  } catch (error) {
    console.warn('Failed to load expiring lots:', error)
    lots.value = []
    lotsTotal.value = 0
  } finally {
    loadingLots.value = false
  }
}

async function loadRecords(page = recordsPage.value) {
  recordsPage.value = page
  loadingRecords.value = true
  try {
    const res = await checkinAPI.getRecords(page, recordsPageSize.value)
    records.value = res.items ?? []
    recordsTotal.value = res.total ?? 0
  } catch (error) {
    console.warn('Failed to load check-in records:', error)
    records.value = []
    recordsTotal.value = 0
  } finally {
    loadingRecords.value = false
  }
}

function shiftMonth(delta: number) {
  const [yStr, mStr] = currentMonth.value.split('-')
  const date = new Date(Date.UTC(Number(yStr), Number(mStr) - 1 + delta, 1))
  const nextMonth = `${date.getUTCFullYear()}-${String(date.getUTCMonth() + 1).padStart(2, '0')}`
  if (nextMonth > serverMonth.value) return
  currentMonth.value = nextMonth
  void loadCalendar(nextMonth)
}

function switchTab(tab: 'lots' | 'records') {
  activeTab.value = tab
  if (tab === 'records' && records.value.length === 0) {
    void loadRecords(1)
  }
}

function selectLotStatus(next: 'all' | ExpiringBalanceLotStatus) {
  lotStatusFilter.value = next
  void loadLots(1)
}

function onLotsPageSizeChange(size: number) {
  lotsPageSize.value = size
  void loadLots(1)
}

function onRecordsPageSizeChange(size: number) {
  recordsPageSize.value = size
  void loadRecords(1)
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
    await refreshAfterCheckin()
  } catch (error) {
    // 请求失败并不代表服务端没有落库：超时、网络抖动、重复点击、
    // 或验证码 token 已被首次尝试消费时，签到可能已经成功。
    // 先重新拉取状态；若服务端已记录今日签到，按成功处理并关闭弹窗。
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

// refreshAfterCheckin 刷新签到成功后所有依赖数据。
// 用 allSettled 而非 all：单个接口失败不应中断其余刷新，否则页面会停在半新半旧的状态。
async function refreshAfterCheckin() {
  await Promise.allSettled([authStore.refreshUser(), loadStatus()])
  await Promise.allSettled([
    loadCalendar(currentMonth.value),
    loadLots(1),
    loadRecords(1)
  ])
}

// recoverIfAlreadyCheckedIn 重新拉取签到状态；若服务端已记录今日签到，
// 则关闭弹窗、提示用户并刷新页面数据，避免停留在“未签到”的旧状态。
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
  await refreshAfterCheckin()
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

onMounted(async () => {
  await loadStatus()
  await Promise.all([loadCalendar(currentMonth.value), loadLots(1)])
})
</script>
