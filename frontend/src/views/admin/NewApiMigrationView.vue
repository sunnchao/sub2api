<template>
  <AppLayout>
    <div class="space-y-6">
      <!-- Site Configuration Card -->
      <div class="card p-6">
        <div class="mb-5 flex flex-wrap items-start justify-between gap-4">
          <div>
            <h2 class="text-base font-semibold text-gray-900 dark:text-white">
              {{ t('newApiMigration.admin.configTitle') }}
            </h2>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
              {{ t('newApiMigration.admin.configDescription') }}
            </p>
          </div>
          <label class="inline-flex cursor-pointer items-center gap-2.5 text-sm font-medium text-gray-800 dark:text-gray-200">
            <input
              v-model="configForm.enabled"
              type="checkbox"
              class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
            />
            <span>{{ t('newApiMigration.admin.enabledLabel') }}</span>
          </label>
        </div>

        <p class="mb-5 rounded-lg bg-gray-50 px-4 py-2.5 text-xs text-gray-600 dark:bg-dark-800 dark:text-gray-400">
          {{ t('newApiMigration.admin.enabledHint') }}
        </p>

        <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
          <div>
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
              {{ t('newApiMigration.admin.baseUrlLabel') }}
            </label>
            <input
              v-model="configForm.base_url"
              type="url"
              class="input w-full"
              :placeholder="t('newApiMigration.admin.baseUrlPlaceholder')"
            />
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ t('newApiMigration.admin.baseUrlHint') }}
            </p>
          </div>

          <div>
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
              {{ t('newApiMigration.admin.siteNameLabel') }}
            </label>
            <input
              v-model="configForm.site_name"
              type="text"
              class="input w-full"
              :placeholder="t('newApiMigration.admin.siteNamePlaceholder')"
            />
          </div>

          <div class="md:col-span-2">
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
              {{ t('newApiMigration.admin.adminTokenLabel') }}
            </label>
            <div class="relative">
              <input
                v-model="configForm.admin_access_token"
                :type="showAdminToken ? 'text' : 'password'"
                autocomplete="off"
                class="input w-full pr-10 font-mono text-sm"
                :placeholder="
                  adminTokenConfigured
                    ? t('newApiMigration.admin.adminTokenPlaceholderConfigured')
                    : t('newApiMigration.admin.adminTokenPlaceholderEmpty')
                "
              />
              <button
                type="button"
                class="absolute inset-y-0 right-0 flex items-center pr-3 text-gray-400 hover:text-gray-600 dark:text-dark-400 dark:hover:text-gray-200"
                @click="showAdminToken = !showAdminToken"
              >
                <Icon :name="showAdminToken ? 'eyeOff' : 'eye'" size="sm" />
              </button>
            </div>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ t('newApiMigration.admin.adminTokenHint') }}
            </p>
          </div>

          <div>
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
              {{ t('newApiMigration.admin.exchangeRateLabel') }}
            </label>
            <input
              v-model.number="configForm.exchange_rate"
              type="number"
              min="0.000001"
              step="0.01"
              class="input w-full"
            />
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ t('newApiMigration.admin.exchangeRateHint') }}
            </p>
          </div>

          <div>
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
              {{ t('newApiMigration.admin.quotaPerUnitLabel') }}
            </label>
            <input
              v-model.number="configForm.quota_per_unit"
              type="number"
              min="1"
              step="1"
              class="input w-full"
            />
            <label class="mt-2 inline-flex cursor-pointer items-center gap-2 text-xs text-gray-700 dark:text-gray-300">
              <input
                v-model="configForm.auto_detect_quota_per_unit"
                type="checkbox"
                class="h-3.5 w-3.5 rounded border-gray-300 text-primary-600"
              />
              <span>{{ t('newApiMigration.admin.autoDetectQuotaLabel') }}</span>
            </label>
          </div>

          <div class="space-y-2 md:col-span-2">
            <label class="inline-flex cursor-pointer items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
              <input
                v-model="configForm.migrate_from_enabled"
                type="checkbox"
                class="h-4 w-4 rounded border-gray-300 text-primary-600"
              />
              <span>{{ t('newApiMigration.admin.migrateFromLabel') }}</span>
            </label>

            <div class="flex flex-wrap items-center gap-2 text-sm text-gray-400 dark:text-gray-500">
              <input
                type="checkbox"
                disabled
                :checked="false"
                class="h-4 w-4 cursor-not-allowed rounded border-gray-300"
              />
              <span>{{ t('newApiMigration.admin.migrateToLabel') }}</span>
              <span
                class="rounded-full bg-gray-100 px-2 py-0.5 text-xs text-gray-500 dark:bg-dark-700 dark:text-gray-400"
              >
                {{ t('newApiMigration.directions.comingSoon') }}
              </span>
              <span class="text-xs">{{ t('newApiMigration.admin.migrateToHint') }}</span>
            </div>

            <div class="pt-1">
              <label class="inline-flex cursor-pointer items-center gap-2 text-xs text-gray-700 dark:text-gray-300">
                <input
                  v-model="configForm.allow_insecure_http"
                  type="checkbox"
                  class="h-3.5 w-3.5 rounded border-gray-300 text-primary-600"
                />
                <span>{{ t('newApiMigration.admin.allowInsecureHttpLabel') }}</span>
              </label>
              <p class="mt-0.5 pl-5 text-xs text-gray-500 dark:text-gray-400">
                {{ t('newApiMigration.admin.allowInsecureHttpHint') }}
              </p>
            </div>
          </div>
        </div>

        <!-- Connection Test Result Banner -->
        <div
          v-if="testResult"
          class="mt-4 rounded-xl border border-emerald-200 bg-emerald-50 p-4 text-sm text-emerald-800 dark:border-emerald-800/50 dark:bg-emerald-900/20 dark:text-emerald-300"
        >
          <p class="font-medium">
            {{
              t('newApiMigration.admin.testSuccess', {
                site: testResult.system_name || testResult.site_url,
                version: testResult.version || '-',
                quotaPerUnit: formatNumber(testResult.quota_per_unit)
              })
            }}
          </p>
          <div class="mt-1.5 flex flex-wrap gap-3 text-xs">
            <span v-if="testResult.admin_user_id > 0">
              {{
                testResult.is_root
                  ? t('newApiMigration.admin.testAdminRoleRoot', {
                      username: testResult.admin_username,
                      id: testResult.admin_user_id
                    })
                  : t('newApiMigration.admin.testAdminRoleAdmin', {
                      username: testResult.admin_username,
                      id: testResult.admin_user_id
                    })
              }}
            </span>
            <span v-if="testResult.user_read_ok">
              · {{ t('newApiMigration.admin.testScopeOk') }}
            </span>
          </div>
          <p v-if="testResult.warning" class="mt-1.5 text-xs text-amber-700 dark:text-amber-300">
            {{ testResult.warning }}
          </p>
        </div>

        <div class="mt-5 flex flex-wrap gap-2.5">
          <button
            type="button"
            class="btn btn-secondary btn-sm"
            :disabled="testingConnection || savingConfig"
            @click="handleTestConnection"
          >
            <Icon name="link" size="sm" class="mr-1.5" />
            {{
              testingConnection
                ? t('newApiMigration.admin.testing')
                : t('newApiMigration.admin.testButton')
            }}
          </button>
          <button
            type="button"
            class="btn btn-primary btn-sm"
            :disabled="savingConfig || testingConnection"
            @click="handleSaveConfig"
          >
            <Icon name="check" size="sm" class="mr-1.5" />
            {{
              savingConfig
                ? t('newApiMigration.admin.saving')
                : t('newApiMigration.admin.saveButton')
            }}
          </button>
        </div>
      </div>

      <!-- Summary Stats Cards -->
      <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <div class="card p-4">
          <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
            {{ t('newApiMigration.admin.stats.totalCount') }}
          </p>
          <p class="mt-1 text-2xl font-bold text-gray-900 dark:text-white">
            {{ stats?.total_count ?? 0 }}
          </p>
        </div>
        <div class="card p-4">
          <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
            {{ t('newApiMigration.admin.stats.completedCount') }}
          </p>
          <p class="mt-1 text-2xl font-bold text-emerald-600 dark:text-emerald-400">
            {{ stats?.completed_count ?? 0 }}
          </p>
        </div>
        <div class="card p-4">
          <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
            {{ t('newApiMigration.admin.stats.totalMigratedBalance') }}
          </p>
          <p class="mt-1 text-2xl font-bold text-primary-600 dark:text-primary-400">
            ${{ formatUsd(stats?.total_migrated_balance ?? 0) }}
          </p>
        </div>
        <div class="card p-4">
          <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
            {{ t('newApiMigration.admin.stats.needsManualReviewCount') }}
          </p>
          <p
            class="mt-1 text-2xl font-bold"
            :class="
              (stats?.needs_manual_review_count ?? 0) > 0
                ? 'text-orange-600 dark:text-orange-400'
                : 'text-gray-900 dark:text-white'
            "
          >
            {{ stats?.needs_manual_review_count ?? 0 }}
          </p>
        </div>
      </div>

      <!-- Audit Records Table -->
      <div class="card overflow-hidden">
        <div
          class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 px-6 py-4 dark:border-dark-700"
        >
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('newApiMigration.admin.recordsTitle') }}
          </h2>
          <div class="flex flex-wrap items-center gap-2">
            <input
              v-model="filters.search"
              type="text"
              class="input w-56 text-sm"
              :placeholder="t('newApiMigration.admin.searchPlaceholder')"
              @keyup.enter="fetchRecords(1)"
            />
            <select
              v-model="filters.status"
              class="input w-36 text-sm"
              @change="fetchRecords(1)"
            >
              <option value="">{{ t('newApiMigration.admin.allStatuses') }}</option>
              <option value="completed">{{ t('newApiMigration.statuses.completed') }}</option>
              <option value="pending">{{ t('newApiMigration.statuses.pending') }}</option>
              <option value="failed">{{ t('newApiMigration.statuses.failed') }}</option>
              <option value="compensated">{{ t('newApiMigration.statuses.compensated') }}</option>
              <option value="needs_manual_review">
                {{ t('newApiMigration.statuses.needs_manual_review') }}
              </option>
            </select>
            <select
              v-model="filters.direction"
              class="input w-44 text-sm"
              @change="fetchRecords(1)"
            >
              <option value="">{{ t('newApiMigration.admin.allDirections') }}</option>
              <option value="from_new_api">{{ t('newApiMigration.directions.from_new_api') }}</option>
              <option value="to_new_api">{{ t('newApiMigration.directions.to_new_api') }}</option>
            </select>
            <button
              type="button"
              class="btn btn-secondary btn-sm"
              :disabled="loadingRecords"
              @click="refreshAuditData"
            >
              <Icon name="refresh" size="sm" :class="{ 'animate-spin': loadingRecords }" />
            </button>
          </div>
        </div>

        <div v-if="loadingRecords && records.length === 0" class="p-8 text-center text-sm text-gray-500">
          {{ t('common.loading') }}
        </div>
        <div
          v-else-if="records.length === 0"
          class="p-8 text-center text-sm text-gray-500 dark:text-gray-400"
        >
          {{ t('newApiMigration.admin.emptyRecords') }}
        </div>
        <div v-else class="overflow-x-auto">
          <table class="min-w-full divide-y divide-gray-200 dark:divide-dark-700">
            <thead class="bg-gray-50 dark:bg-dark-800/60">
              <tr>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase text-gray-500 dark:text-gray-400">
                  {{ t('newApiMigration.admin.columns.time') }}
                </th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase text-gray-500 dark:text-gray-400">
                  {{ t('newApiMigration.admin.columns.direction') }}
                </th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase text-gray-500 dark:text-gray-400">
                  {{ t('newApiMigration.admin.columns.sub2apiUser') }}
                </th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase text-gray-500 dark:text-gray-400">
                  {{ t('newApiMigration.admin.columns.newApiUser') }}
                </th>
                <th class="px-4 py-3 text-right text-xs font-medium uppercase text-gray-500 dark:text-gray-400">
                  {{ t('newApiMigration.admin.columns.quotaChange') }}
                </th>
                <th class="px-4 py-3 text-right text-xs font-medium uppercase text-gray-500 dark:text-gray-400">
                  {{ t('newApiMigration.admin.columns.balanceChange') }}
                </th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase text-gray-500 dark:text-gray-400">
                  {{ t('newApiMigration.admin.columns.status') }}
                </th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase text-gray-500 dark:text-gray-400">
                  {{ t('newApiMigration.admin.columns.clientIp') }}
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
                <td class="whitespace-nowrap px-4 py-3 text-xs">
                  <div class="font-medium text-gray-900 dark:text-white">
                    {{ rec.sub2api_user_email || `#${rec.sub2api_user_id}` }}
                  </div>
                  <div class="text-gray-400">UID #{{ rec.sub2api_user_id }}</div>
                </td>
                <td class="whitespace-nowrap px-4 py-3 text-xs">
                  <div class="font-medium text-gray-900 dark:text-white">
                    {{ rec.new_api_username || '-' }}
                    <span class="font-normal text-gray-400">#{{ rec.new_api_user_id }}</span>
                  </div>
                  <div v-if="rec.new_api_email" class="text-gray-400">{{ rec.new_api_email }}</div>
                </td>
                <td class="whitespace-nowrap px-4 py-3 text-right font-mono text-xs">
                  <div class="text-gray-900 dark:text-white">-{{ formatNumber(rec.new_api_quota_delta) }}</div>
                  <div class="text-gray-400">
                    {{ formatNumber(rec.new_api_quota_before) }} -> {{ formatNumber(rec.new_api_quota_after) }}
                  </div>
                </td>
                <td class="whitespace-nowrap px-4 py-3 text-right font-mono text-xs">
                  <div class="font-semibold text-emerald-600 dark:text-emerald-400">
                    +${{ formatUsd(rec.sub2api_balance_delta) }}
                  </div>
                  <div class="text-gray-400">
                    ${{ formatUsd(rec.sub2api_balance_before) }} -> ${{ formatUsd(rec.sub2api_balance_after) }}
                  </div>
                </td>
                <td class="px-4 py-3 text-xs">
                  <span :class="statusBadgeClass(rec.status)" :title="rec.error_message || undefined">
                    {{ formatStatus(rec.status) }}
                  </span>
                  <p
                    v-if="rec.error_message"
                    class="mt-1 max-w-xs truncate text-[11px] text-red-600 dark:text-red-400"
                    :title="rec.error_message"
                  >
                    {{ rec.error_message }}
                  </p>
                </td>
                <td class="whitespace-nowrap px-4 py-3 font-mono text-xs text-gray-500 dark:text-gray-400">
                  {{ rec.client_ip || '-' }}
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
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Pagination from '@/components/common/Pagination.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores/app'
import {
  adminNewApiMigrationAPI,
  type NewApiMigrationStats,
  type NewApiMigrationTestConnectionResult
} from '@/api/admin/newApiMigration'
import type {
  NewApiMigrationDirection,
  NewApiMigrationRecord,
  NewApiMigrationStatus
} from '@/api/newApiMigration'
import { formatDateTime } from '@/utils/format'

const { t } = useI18n()
const appStore = useAppStore()

const configForm = reactive({
  enabled: false,
  base_url: '',
  site_name: '',
  admin_access_token: '',
  quota_per_unit: 500000,
  auto_detect_quota_per_unit: true,
  exchange_rate: 1.0,
  migrate_from_enabled: true,
  migrate_to_enabled: false,
  allow_insecure_http: false
})

const adminTokenConfigured = ref(false)
const showAdminToken = ref(false)
const savingConfig = ref(false)
const testingConnection = ref(false)
const testResult = ref<NewApiMigrationTestConnectionResult | null>(null)

const stats = ref<NewApiMigrationStats | null>(null)
const records = ref<NewApiMigrationRecord[]>([])
const loadingRecords = ref(false)
const recordsPage = ref(1)
const recordsPageSize = ref(20)
const recordsTotal = ref(0)

const filters = reactive<{
  status: NewApiMigrationStatus | ''
  direction: NewApiMigrationDirection | ''
  search: string
}>({
  status: '',
  direction: '',
  search: ''
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

function extractErrorMessage(err: unknown): string {
  if (err && typeof err === 'object') {
    const maybe = err as { response?: { data?: { message?: string } }; message?: string }
    if (maybe.response?.data?.message) return maybe.response.data.message
    if (maybe.message) return maybe.message
  }
  return t('common.error')
}

async function fetchConfig() {
  try {
    const cfg = await adminNewApiMigrationAPI.getConfig()
    configForm.enabled = cfg.enabled
    configForm.base_url = cfg.base_url || ''
    configForm.site_name = cfg.site_name || ''
    configForm.admin_access_token = ''
    configForm.quota_per_unit = cfg.quota_per_unit || 500000
    configForm.auto_detect_quota_per_unit = cfg.auto_detect_quota_per_unit
    configForm.exchange_rate = cfg.exchange_rate || 1.0
    configForm.migrate_from_enabled = cfg.migrate_from_enabled
    configForm.migrate_to_enabled = false
    configForm.allow_insecure_http = cfg.allow_insecure_http
    adminTokenConfigured.value = cfg.admin_access_token_configured
  } catch (err) {
    appStore.showError(extractErrorMessage(err))
  }
}

async function handleSaveConfig() {
  savingConfig.value = true
  try {
    const updated = await adminNewApiMigrationAPI.updateConfig({
      enabled: configForm.enabled,
      base_url: configForm.base_url.trim(),
      site_name: configForm.site_name.trim(),
      admin_access_token: configForm.admin_access_token.trim() || undefined,
      quota_per_unit: Number(configForm.quota_per_unit),
      auto_detect_quota_per_unit: configForm.auto_detect_quota_per_unit,
      exchange_rate: Number(configForm.exchange_rate),
      migrate_from_enabled: configForm.migrate_from_enabled,
      migrate_to_enabled: false,
      allow_insecure_http: configForm.allow_insecure_http
    })
    adminTokenConfigured.value = updated.admin_access_token_configured
    configForm.base_url = updated.base_url
    configForm.site_name = updated.site_name
    configForm.admin_access_token = ''
    appStore.showSuccess(t('newApiMigration.admin.saveSuccess'))
    void appStore.fetchPublicSettings(true)
  } catch (err) {
    appStore.showError(extractErrorMessage(err))
  } finally {
    savingConfig.value = false
  }
}

async function handleTestConnection() {
  testingConnection.value = true
  testResult.value = null
  try {
    const res = await adminNewApiMigrationAPI.testConnection({
      base_url: configForm.base_url.trim() || undefined,
      admin_access_token: configForm.admin_access_token.trim() || undefined,
      allow_insecure_http: configForm.allow_insecure_http
    })
    testResult.value = res
    if (res.quota_per_unit > 0 && configForm.auto_detect_quota_per_unit) {
      configForm.quota_per_unit = res.quota_per_unit
    }
  } catch (err) {
    appStore.showError(extractErrorMessage(err))
  } finally {
    testingConnection.value = false
  }
}

async function fetchStats() {
  try {
    stats.value = await adminNewApiMigrationAPI.getStats()
  } catch (err) {
    appStore.showError(extractErrorMessage(err))
  }
}

async function fetchRecords(page = 1) {
  loadingRecords.value = true
  try {
    const res = await adminNewApiMigrationAPI.listRecords({
      page,
      page_size: recordsPageSize.value,
      status: filters.status,
      direction: filters.direction,
      search: filters.search.trim()
    })
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

function refreshAuditData() {
  void Promise.all([fetchStats(), fetchRecords(recordsPage.value)])
}

onMounted(() => {
  void Promise.all([fetchConfig(), fetchStats(), fetchRecords(1)])
})
</script>
