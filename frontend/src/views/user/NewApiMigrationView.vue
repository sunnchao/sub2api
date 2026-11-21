<template>
  <AppLayout>
    <div class="mx-auto max-w-3xl space-y-6">
      <!-- Current Balance & Source Site Banner -->
      <div class="card overflow-hidden">
        <div class="bg-gradient-to-br from-primary-500 to-primary-600 px-6 py-7 text-white">
          <div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <p class="text-sm font-medium text-primary-100">
                {{ t('newApiMigration.user.currentBalance') }}
              </p>
              <p class="mt-1 text-4xl font-bold">
                ${{ formatUsd(user?.balance ?? 0) }}
              </p>
              <p class="mt-2 text-xs text-primary-100/90">
                {{ t('newApiMigration.user.oneTimeFullNote') }}
              </p>
            </div>
            <div
              v-if="info?.enabled"
              class="rounded-xl bg-white/15 px-4 py-3 text-sm backdrop-blur-sm sm:text-right"
            >
              <p class="text-xs text-primary-100">{{ t('newApiMigration.user.sourceSite') }}</p>
              <p class="mt-0.5 font-semibold">{{ info.site_name || 'New API' }}</p>
              <p v-if="info.site_url" class="mt-0.5 break-all text-xs text-primary-100/80">
                {{ info.site_url }}
              </p>
              <div class="mt-2 flex flex-wrap gap-2 sm:justify-end">
                <span class="inline-flex items-center rounded-md bg-white/20 px-2 py-0.5 text-xs">
                  {{ t('newApiMigration.user.exchangeRate') }}: {{ info.exchange_rate }}x
                </span>
                <span class="inline-flex items-center rounded-md bg-white/20 px-2 py-0.5 text-xs">
                  1 USD = {{ formatNumber(info.quota_per_unit) }} Quota
                </span>
              </div>
            </div>
          </div>
        </div>
        <div
          class="flex flex-wrap items-center justify-between gap-2 border-t border-gray-100 bg-gray-50 px-6 py-3 text-xs text-gray-600 dark:border-dark-700 dark:bg-dark-800/70 dark:text-gray-400"
        >
          <div class="flex items-center gap-2">
            <span
              class="inline-flex items-center rounded-full bg-emerald-100 px-2.5 py-0.5 font-medium text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300"
            >
              {{ t('newApiMigration.directions.from_new_api') }}
            </span>
            <span
              class="inline-flex items-center rounded-full bg-gray-200 px-2.5 py-0.5 font-medium text-gray-600 dark:bg-dark-700 dark:text-gray-400"
            >
              {{ t('newApiMigration.directions.to_new_api') }} ·
              {{ t('newApiMigration.directions.comingSoon') }}
            </span>
          </div>
          <span>{{ t('newApiMigration.user.bidirectionalRoadmap') }}</span>
        </div>
      </div>

      <!-- Loading Info -->
      <div v-if="loadingInfo" class="card p-8 text-center text-sm text-gray-500 dark:text-gray-400">
        {{ t('common.loading') }}
      </div>

      <!-- Disabled State -->
      <div
        v-else-if="!info?.enabled || !info?.migrate_from_enabled"
        class="card border-amber-200 bg-amber-50/70 p-6 dark:border-amber-800/50 dark:bg-amber-900/20"
      >
        <div class="flex items-start gap-3">
          <Icon name="exclamationTriangle" size="md" class="mt-0.5 flex-shrink-0 text-amber-600 dark:text-amber-400" />
          <div>
            <h3 class="text-sm font-semibold text-amber-900 dark:text-amber-200">
              {{ t('newApiMigration.user.disabledTitle') }}
            </h3>
            <p class="mt-1 text-sm text-amber-800 dark:text-amber-300">
              {{ t('newApiMigration.user.disabledDescription') }}
            </p>
          </div>
        </div>
      </div>

      <!-- Main Migration Card -->
      <template v-else>
        <!-- Step 1: Query Balance by New API Access Token -->
        <div class="card">
          <div class="p-6">
            <form class="space-y-4" @submit.prevent="handlePreview">
              <div>
                <label for="new-api-access-token" class="input-label">
                  {{ t('newApiMigration.user.accessTokenLabel') }}
                </label>
                <div class="relative mt-1">
                  <div class="pointer-events-none absolute inset-y-0 left-0 flex items-center pl-3.5">
                    <Icon name="key" size="md" class="text-gray-400 dark:text-dark-500" />
                  </div>
                  <input
                    id="new-api-access-token"
                    v-model="accessToken"
                    :type="showToken ? 'text' : 'password'"
                    required
                    autocomplete="off"
                    :placeholder="t('newApiMigration.user.accessTokenPlaceholder')"
                    :disabled="querying || executing"
                    class="input py-2.5 pl-11 pr-11 font-mono text-sm"
                  />
                  <button
                    type="button"
                    class="absolute inset-y-0 right-0 flex items-center pr-3.5 text-gray-400 hover:text-gray-600 dark:text-dark-400 dark:hover:text-gray-200"
                    @click="showToken = !showToken"
                  >
                    <Icon :name="showToken ? 'eyeOff' : 'eye'" size="sm" />
                  </button>
                </div>
                <p class="input-hint mt-1.5">
                  {{ t('newApiMigration.user.accessTokenHint') }}
                </p>
              </div>

              <button
                type="submit"
                :disabled="!accessToken.trim() || querying || executing"
                class="btn btn-primary w-full py-2.5"
              >
                <Icon name="search" size="sm" class="mr-2" />
                {{
                  querying
                    ? t('newApiMigration.user.querying')
                    : t('newApiMigration.user.queryButton')
                }}
              </button>
            </form>
          </div>
        </div>

        <!-- Error Message -->
        <div
          v-if="errorMessage"
          class="card border-red-200 bg-red-50 p-5 dark:border-red-800/50 dark:bg-red-900/20"
        >
          <div class="flex items-start gap-3">
            <Icon name="exclamationCircle" size="md" class="mt-0.5 flex-shrink-0 text-red-600 dark:text-red-400" />
            <p class="text-sm text-red-800 dark:text-red-300">{{ errorMessage }}</p>
          </div>
        </div>

        <!-- Success Result Banner -->
        <div
          v-if="executeResult"
          class="card border-emerald-200 bg-emerald-50 p-6 dark:border-emerald-800/50 dark:bg-emerald-900/20"
        >
          <div class="flex items-start gap-3">
            <div
              class="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-xl bg-emerald-100 dark:bg-emerald-900/40"
            >
              <Icon name="checkCircle" size="md" class="text-emerald-600 dark:text-emerald-400" />
            </div>
            <div class="flex-1">
              <h3 class="text-sm font-semibold text-emerald-900 dark:text-emerald-200">
                {{ t('newApiMigration.user.successTitle') }}
              </h3>
              <p class="mt-1 text-sm text-emerald-800 dark:text-emerald-300">
                {{
                  t('newApiMigration.user.successMessage', {
                    credit: `$${formatUsd(executeResult.record.sub2api_balance_delta)}`,
                    account: executeResult.record.new_api_username || `#${executeResult.record.new_api_user_id}`,
                    uid: executeResult.record.new_api_user_id,
                    newBalance: `$${formatUsd(executeResult.new_balance)}`
                  })
                }}
              </p>
            </div>
          </div>
        </div>

        <!-- Step 2: Preview & Confirm Card -->
        <div v-if="previewResult" class="card overflow-hidden">
          <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
            <h3 class="text-base font-semibold text-gray-900 dark:text-white">
              {{ t('newApiMigration.user.previewTitle') }}
            </h3>
          </div>
          <div class="space-y-5 p-6">
            <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <div class="rounded-xl bg-gray-50 p-4 dark:bg-dark-800">
                <p class="text-xs text-gray-500 dark:text-gray-400">
                  {{ t('newApiMigration.user.newApiAccount') }}
                </p>
                <p class="mt-1 text-sm font-semibold text-gray-900 dark:text-white">
                  {{ previewResult.new_api_username }}
                  <span v-if="previewResult.new_api_display_name" class="font-normal text-gray-500">
                    ({{ previewResult.new_api_display_name }})
                  </span>
                </p>
                <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                  {{ t('newApiMigration.user.newApiUserId') }}: #{{ previewResult.new_api_user_id }}
                  <span v-if="previewResult.new_api_email"> · {{ previewResult.new_api_email }}</span>
                </p>
              </div>

              <div class="rounded-xl bg-gray-50 p-4 dark:bg-dark-800">
                <p class="text-xs text-gray-500 dark:text-gray-400">
                  {{ t('newApiMigration.user.regularBalance') }}
                </p>
                <p class="mt-1 text-lg font-bold text-gray-900 dark:text-white">
                  ${{ formatUsd(previewResult.raw_usd) }}
                </p>
                <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                  {{ formatNumber(previewResult.regular_quota) }} Quota
                </p>
              </div>

              <div class="rounded-xl bg-emerald-50/70 p-4 dark:bg-emerald-900/20">
                <p class="text-xs text-emerald-700 dark:text-emerald-300">
                  {{ t('newApiMigration.user.estimatedCredit') }}
                </p>
                <p class="mt-1 text-xl font-bold text-emerald-700 dark:text-emerald-300">
                  +${{ formatUsd(previewResult.estimated_credit) }}
                </p>
                <p class="mt-0.5 text-xs text-emerald-700/80 dark:text-emerald-400/80">
                  {{ t('newApiMigration.user.exchangeRate') }}: {{ previewResult.exchange_rate }}x
                </p>
              </div>

              <div class="rounded-xl bg-gray-50 p-4 dark:bg-dark-800">
                <p class="text-xs text-gray-500 dark:text-gray-400">
                  {{ t('newApiMigration.user.estimatedNewBalance') }}
                </p>
                <p class="mt-1 text-xl font-bold text-gray-900 dark:text-white">
                  ${{ formatUsd(previewResult.estimated_new_balance) }}
                </p>
                <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                  ${{ formatUsd(previewResult.current_balance) }} -> ${{
                    formatUsd(previewResult.estimated_new_balance)
                  }}
                </p>
              </div>
            </div>

            <!-- Gift quota informational note -->
            <div
              v-if="previewResult.gift_quota > 0"
              class="rounded-xl border border-blue-200 bg-blue-50/70 p-4 text-sm text-blue-800 dark:border-blue-800/50 dark:bg-blue-900/20 dark:text-blue-300"
            >
              <div class="flex items-start gap-2.5">
                <Icon name="infoCircle" size="sm" class="mt-0.5 flex-shrink-0" />
                <span>
                  {{
                    t('newApiMigration.user.giftQuotaNotice', {
                      amount: `$${formatUsd(previewResult.gift_quota / previewResult.quota_per_unit)} (${formatNumber(previewResult.gift_quota)} Quota)`
                    })
                  }}
                </span>
              </div>
            </div>

            <!-- Cannot migrate notice -->
            <div
              v-if="!previewResult.can_migrate"
              class="rounded-xl border border-amber-200 bg-amber-50 p-4 text-sm text-amber-800 dark:border-amber-800/50 dark:bg-amber-900/20 dark:text-amber-300"
            >
              <div class="flex items-start gap-2.5">
                <Icon name="exclamationTriangle" size="sm" class="mt-0.5 flex-shrink-0" />
                <span>{{ formatCannotMigrateReason(previewResult.cannot_migrate_reason) }}</span>
              </div>
            </div>

            <!-- Irreversible warning + Confirm button -->
            <template v-else>
              <div
                class="rounded-xl border border-amber-200 bg-amber-50/70 p-4 text-xs text-amber-800 dark:border-amber-800/50 dark:bg-amber-900/20 dark:text-amber-300"
              >
                <div class="flex items-start gap-2.5">
                  <Icon name="exclamationTriangle" size="sm" class="mt-0.5 flex-shrink-0" />
                  <span>{{ t('newApiMigration.user.oneTimeWarning') }}</span>
                </div>
              </div>

              <button
                type="button"
                class="btn btn-primary w-full py-3"
                :disabled="executing"
                @click="showConfirmDialog = true"
              >
                <Icon name="checkCircle" size="md" class="mr-2" />
                {{
                  executing
                    ? t('newApiMigration.user.migrating')
                    : t('newApiMigration.user.confirmButton')
                }}
              </button>
            </template>
          </div>
        </div>
      </template>

      <!-- User Migration History -->
      <div class="card overflow-hidden">
        <div
          class="flex items-center justify-between border-b border-gray-100 px-6 py-4 dark:border-dark-700"
        >
          <h3 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('newApiMigration.user.historyTitle') }}
          </h3>
          <button
            type="button"
            class="btn btn-ghost btn-sm"
            :disabled="loadingRecords"
            @click="fetchRecords(recordsPage)"
          >
            <Icon name="refresh" size="sm" :class="{ 'animate-spin': loadingRecords }" />
          </button>
        </div>

        <div v-if="loadingRecords && records.length === 0" class="p-8 text-center text-sm text-gray-500">
          {{ t('common.loading') }}
        </div>
        <div
          v-else-if="records.length === 0"
          class="p-8 text-center text-sm text-gray-500 dark:text-gray-400"
        >
          {{ t('newApiMigration.user.historyEmpty') }}
        </div>
        <div v-else class="overflow-x-auto">
          <table class="min-w-full divide-y divide-gray-200 dark:divide-dark-700">
            <thead class="bg-gray-50 dark:bg-dark-800/60">
              <tr>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase text-gray-500 dark:text-gray-400">
                  {{ t('newApiMigration.user.columns.time') }}
                </th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase text-gray-500 dark:text-gray-400">
                  {{ t('newApiMigration.user.columns.direction') }}
                </th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase text-gray-500 dark:text-gray-400">
                  {{ t('newApiMigration.user.columns.newApiAccount') }}
                </th>
                <th class="px-4 py-3 text-right text-xs font-medium uppercase text-gray-500 dark:text-gray-400">
                  {{ t('newApiMigration.user.columns.newApiQuota') }}
                </th>
                <th class="px-4 py-3 text-right text-xs font-medium uppercase text-gray-500 dark:text-gray-400">
                  {{ t('newApiMigration.user.columns.creditedAmount') }}
                </th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase text-gray-500 dark:text-gray-400">
                  {{ t('newApiMigration.user.columns.status') }}
                </th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 bg-white dark:divide-dark-700 dark:bg-dark-900">
              <tr v-for="rec in records" :key="rec.id">
                <td class="whitespace-nowrap px-4 py-3 text-xs text-gray-600 dark:text-gray-400">
                  {{ formatDateTime(rec.created_at) }}
                </td>
                <td class="whitespace-nowrap px-4 py-3 text-xs text-gray-700 dark:text-gray-300">
                  {{ formatDirection(rec.direction) }}
                </td>
                <td class="whitespace-nowrap px-4 py-3 text-xs text-gray-900 dark:text-white">
                  {{ rec.new_api_username || '-' }}
                  <span class="text-gray-400">#{{ rec.new_api_user_id }}</span>
                </td>
                <td class="whitespace-nowrap px-4 py-3 text-right font-mono text-xs text-gray-700 dark:text-gray-300">
                  -{{ formatNumber(rec.new_api_quota_delta) }}
                </td>
                <td class="whitespace-nowrap px-4 py-3 text-right font-mono text-xs font-semibold text-emerald-600 dark:text-emerald-400">
                  +${{ formatUsd(rec.sub2api_balance_delta) }}
                </td>
                <td class="whitespace-nowrap px-4 py-3 text-xs">
                  <span :class="statusBadgeClass(rec.status)">
                    {{ formatStatus(rec.status) }}
                  </span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <Pagination
          v-if="recordsTotal > recordsPageSize"
          :page="recordsPage"
          :page-size="recordsPageSize"
          :total="recordsTotal"
          @update:page="fetchRecords"
          @update:page-size="handlePageSizeChange"
        />
      </div>
    </div>

    <ConfirmDialog
      :show="showConfirmDialog"
      :title="t('newApiMigration.user.confirmDialogTitle')"
      :message="confirmDialogMessage"
      :confirm-text="t('newApiMigration.user.confirmButton')"
      danger
      @confirm="handleExecute"
      @cancel="showConfirmDialog = false"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import {
  newApiMigrationAPI,
  type NewApiMigrationDirection,
  type NewApiMigrationExecuteResult,
  type NewApiMigrationPreviewResult,
  type NewApiMigrationRecord,
  type NewApiMigrationStatus,
  type NewApiMigrationUserInfo
} from '@/api/newApiMigration'
import { formatDateTime } from '@/utils/format'

const { t } = useI18n()
const authStore = useAuthStore()
const appStore = useAppStore()

const user = computed(() => authStore.user)

const info = ref<NewApiMigrationUserInfo | null>(null)
const loadingInfo = ref(true)

const accessToken = ref('')
const showToken = ref(false)
const querying = ref(false)
const executing = ref(false)
const showConfirmDialog = ref(false)

const previewResult = ref<NewApiMigrationPreviewResult | null>(null)
const executeResult = ref<NewApiMigrationExecuteResult | null>(null)
const errorMessage = ref('')

const records = ref<NewApiMigrationRecord[]>([])
const loadingRecords = ref(false)
const recordsPage = ref(1)
const recordsPageSize = ref(20)
const recordsTotal = ref(0)

const confirmDialogMessage = computed(() => {
  if (!previewResult.value) return ''
  return t('newApiMigration.user.confirmDialogMessage', {
    rawUsd: `$${formatUsd(previewResult.value.raw_usd)}`,
    account: previewResult.value.new_api_username || `#${previewResult.value.new_api_user_id}`,
    uid: previewResult.value.new_api_user_id,
    credit: `$${formatUsd(previewResult.value.estimated_credit)}`
  })
})

function formatUsd(value: number): string {
  if (!Number.isFinite(value)) return '0.00'
  const fixed = value.toFixed(6).replace(/(\.\d{2})\d*?(0+)$/, '$1')
  return fixed.endsWith('.') ? `${fixed}00` : fixed
}

function formatNumber(value: number): string {
  if (!Number.isFinite(value)) return '0'
  return Math.round(value).toLocaleString()
}

function formatDirection(direction: NewApiMigrationDirection): string {
  return direction === 'to_new_api'
    ? t('newApiMigration.directions.to_new_api')
    : t('newApiMigration.directions.from_new_api')
}

function formatStatus(status: NewApiMigrationStatus): string {
  switch (status) {
    case 'completed':
      return t('newApiMigration.statuses.completed')
    case 'pending':
      return t('newApiMigration.statuses.pending')
    case 'failed':
      return t('newApiMigration.statuses.failed')
    case 'compensated':
      return t('newApiMigration.statuses.compensated')
    case 'needs_manual_review':
      return t('newApiMigration.statuses.needs_manual_review')
    default:
      return status
  }
}

function statusBadgeClass(status: NewApiMigrationStatus): string {
  const base = 'inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium'
  switch (status) {
    case 'completed':
      return `${base} bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300`
    case 'pending':
      return `${base} bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300`
    case 'compensated':
      return `${base} bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300`
    case 'needs_manual_review':
      return `${base} bg-orange-100 text-orange-700 dark:bg-orange-900/30 dark:text-orange-300`
    default:
      return `${base} bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300`
  }
}

function formatCannotMigrateReason(reason?: string): string {
  switch (reason) {
    case 'NEW_API_USER_ALREADY_MIGRATED':
      return t('newApiMigration.reasons.NEW_API_USER_ALREADY_MIGRATED')
    case 'NEW_API_MIGRATION_IN_PROGRESS':
      return t('newApiMigration.reasons.NEW_API_MIGRATION_IN_PROGRESS')
    case 'NEW_API_MIGRATION_NEEDS_REVIEW':
      return t('newApiMigration.reasons.NEW_API_MIGRATION_NEEDS_REVIEW')
    case 'NEW_API_QUOTA_EMPTY':
      return t('newApiMigration.reasons.NEW_API_QUOTA_EMPTY')
    default:
      return reason || ''
  }
}

function extractErrorMessage(err: unknown): string {
  if (err && typeof err === 'object') {
    const maybe = err as { response?: { data?: { message?: string } }; message?: string }
    if (maybe.response?.data?.message) return maybe.response.data.message
    if (maybe.message) return maybe.message
  }
  return t('common.error')
}

async function fetchInfo() {
  loadingInfo.value = true
  try {
    info.value = await newApiMigrationAPI.getInfo()
  } catch (err) {
    errorMessage.value = extractErrorMessage(err)
  } finally {
    loadingInfo.value = false
  }
}

async function fetchRecords(page = 1) {
  loadingRecords.value = true
  try {
    const res = await newApiMigrationAPI.listRecords(page, recordsPageSize.value)
    records.value = res.items || []
    recordsTotal.value = res.total || 0
    recordsPage.value = res.page || page
  } catch (err) {
    appStore.showError(extractErrorMessage(err))
  } finally {
    loadingRecords.value = false
  }
}

function handlePageSizeChange(size: number) {
  recordsPageSize.value = size
  void fetchRecords(1)
}

async function handlePreview() {
  const token = accessToken.value.trim()
  if (!token) return
  querying.value = true
  errorMessage.value = ''
  executeResult.value = null
  try {
    previewResult.value = await newApiMigrationAPI.preview(token)
  } catch (err) {
    previewResult.value = null
    errorMessage.value = extractErrorMessage(err)
  } finally {
    querying.value = false
  }
}

async function handleExecute() {
  showConfirmDialog.value = false
  const token = accessToken.value.trim()
  if (!token || !previewResult.value) return
  executing.value = true
  errorMessage.value = ''
  try {
    const res = await newApiMigrationAPI.execute(token, previewResult.value.new_api_user_id)
    executeResult.value = res
    previewResult.value = null
    accessToken.value = ''
    appStore.showSuccess(t('newApiMigration.user.successTitle'))
    await Promise.allSettled([authStore.refreshUser(), fetchRecords(1)])
  } catch (err) {
    errorMessage.value = extractErrorMessage(err)
    appStore.showError(errorMessage.value)
    // 即使遇到前端超时或网络断连，后端可能已完成入账，主动刷新余额与迁移记录以便用户立即核对结果
    await Promise.allSettled([authStore.refreshUser(), fetchRecords(1)])
  } finally {
    executing.value = false
  }
}

onMounted(() => {
  void Promise.all([fetchInfo(), fetchRecords(1)])
})
</script>
