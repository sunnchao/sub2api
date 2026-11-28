<template>
  <AppLayout>
    <div class="space-y-6">
      <!-- Top Header Controls -->
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 class="text-xl font-bold text-gray-900 dark:text-white">
            {{ t('admin.checkin.title') }}
          </h1>
          <p class="mt-0.5 text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.checkin.description') }}
          </p>
        </div>

        <div class="flex flex-wrap items-center gap-2">
          <Select
            v-model="rangeDays"
            :options="rangeOptions"
            class="w-36"
            @change="handleRangeChange"
          />
          <router-link
            to="/admin/settings"
            class="btn btn-secondary btn-sm"
          >
            {{ t('admin.checkin.configLink') }}
          </router-link>
          <button
            type="button"
            class="btn btn-secondary btn-sm"
            :disabled="loadingStats || loadingRecords"
            @click="refreshAll"
          >
            <Icon name="refresh" size="sm" :class="{ 'animate-spin': loadingStats || loadingRecords }" />
          </button>
        </div>
      </div>

      <!-- Summary KPI Cards -->
      <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <div class="card p-4">
          <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
            {{ t('admin.checkin.stats.todayCheckins') }}
          </p>
          <p class="mt-1 text-2xl font-bold text-gray-900 dark:text-white">
            {{ stats?.today_checkins ?? 0 }}
          </p>
          <p class="mt-1 text-xs text-emerald-600 dark:text-emerald-400">
            {{ t('admin.checkin.stats.todayGranted') }}: ${{ formatUsd(stats?.today_granted ?? 0) }}
          </p>
        </div>

        <div class="card p-4">
          <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
            {{ t('admin.checkin.stats.rangeTotalGranted', { days: rangeDays }) }}
          </p>
          <p class="mt-1 text-2xl font-bold text-primary-600 dark:text-primary-400">
            ${{ formatUsd(stats?.range_total_granted ?? 0) }}
          </p>
        </div>

        <div class="card p-4">
          <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
            {{ t('admin.checkin.stats.consumptionRate') }}
          </p>
          <p class="mt-1 text-2xl font-bold text-emerald-600 dark:text-emerald-400">
            {{ formatPercent(stats?.consumption_rate ?? 0) }}
          </p>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            ${{ formatUsd(stats?.range_total_consumed ?? 0) }}
          </p>
        </div>

        <div class="card p-4">
          <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
            {{ t('admin.checkin.stats.expirationRate') }}
          </p>
          <p class="mt-1 text-2xl font-bold text-amber-600 dark:text-amber-400">
            {{ formatPercent(stats?.expiration_rate ?? 0) }}
          </p>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            ${{ formatUsd(stats?.range_total_expired ?? 0) }}
          </p>
        </div>
      </div>

      <!-- Daily Budget Progress Bar -->
      <div class="card p-5">
        <div class="flex flex-wrap items-center justify-between gap-2 text-sm">
          <span class="font-medium text-gray-900 dark:text-white">
            {{ t('admin.checkin.stats.budgetProgress') }}
          </span>
          <span v-if="(stats?.daily_budget_limit ?? 0) > 0" class="font-mono text-gray-700 dark:text-gray-300">
            ${{ formatUsd(stats?.today_granted ?? 0) }} / ${{ formatUsd(stats?.daily_budget_limit ?? 0) }}
            ({{ Math.min(100, stats?.daily_budget_percent ?? 0).toFixed(1) }}%)
          </span>
          <span v-else class="text-xs font-medium text-gray-500 dark:text-gray-400">
            ${{ formatUsd(stats?.today_granted ?? 0) }} · {{ t('admin.checkin.stats.budgetUnlimited') }}
          </span>
        </div>
        <div
          v-if="(stats?.daily_budget_limit ?? 0) > 0"
          class="mt-3 h-2.5 w-full overflow-hidden rounded-full bg-gray-200 dark:bg-dark-700"
        >
          <div
            data-testid="admin-checkin-budget-bar"
            class="h-full rounded-full transition-all"
            :class="budgetBarClass"
            :style="{ width: `${Math.min(100, Math.max(0, stats?.daily_budget_percent ?? 0))}%` }"
          />
        </div>
      </div>

      <!-- Daily Trend Table -->
      <div v-if="stats?.daily_trend && stats.daily_trend.length > 0" class="card overflow-hidden">
        <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('admin.checkin.trendTitle') }}
          </h2>
        </div>
        <div class="table-container">
          <table class="table">
            <thead>
              <tr>
                <th>{{ t('admin.checkin.columns.date') }}</th>
                <th>{{ t('admin.checkin.trendColumns.checkins') }}</th>
                <th>{{ t('admin.checkin.trendColumns.granted') }}</th>
                <th>{{ t('admin.checkin.trendColumns.consumed') }}</th>
                <th>{{ t('admin.checkin.trendColumns.expired') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="item in stats.daily_trend" :key="item.date">
                <td class="font-mono text-sm text-gray-900 dark:text-white">{{ item.date }}</td>
                <td class="font-mono text-sm text-gray-700 dark:text-gray-300">{{ item.checkin_count }}</td>
                <td class="font-mono text-sm text-primary-600 dark:text-primary-400">
                  ${{ formatUsd(item.granted_amount) }}
                </td>
                <td class="font-mono text-sm text-emerald-600 dark:text-emerald-400">
                  ${{ formatUsd(item.consumed_amount) }}
                </td>
                <td class="font-mono text-sm text-amber-600 dark:text-amber-400">
                  ${{ formatUsd(item.expired_amount) }}
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>

      <!-- Filtered Records Table -->
      <div class="card overflow-hidden">
        <div
          class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 px-6 py-4 dark:border-dark-700"
        >
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('admin.checkin.recordsTitle') }}
          </h2>
          <div class="flex flex-wrap items-center gap-2">
            <input
              v-model.trim="filters.search"
              type="text"
              data-testid="filter-search"
              class="input w-48 text-sm"
              :placeholder="t('admin.checkin.filters.searchPlaceholder')"
              @keyup.enter="fetchRecords(1)"
            />
            <input
              v-model.trim="filters.userId"
              type="number"
              min="1"
              step="1"
              data-testid="filter-user-id"
              class="input w-28 text-sm"
              :placeholder="t('admin.checkin.filters.userIdPlaceholder')"
              @keyup.enter="fetchRecords(1)"
            />
            <input
              v-model.trim="filters.clientIp"
              type="text"
              data-testid="filter-client-ip"
              class="input w-36 text-sm"
              :placeholder="t('admin.checkin.filters.ipPlaceholder')"
              @keyup.enter="fetchRecords(1)"
            />
            <input
              v-model="filters.startDate"
              type="date"
              data-testid="filter-start-date"
              class="input w-36 text-sm"
              :aria-label="t('admin.checkin.filters.startDate')"
              @change="fetchRecords(1)"
            />
            <input
              v-model="filters.endDate"
              type="date"
              data-testid="filter-end-date"
              class="input w-36 text-sm"
              :aria-label="t('admin.checkin.filters.endDate')"
              @change="fetchRecords(1)"
            />
            <button
              type="button"
              data-testid="filter-apply-btn"
              class="btn btn-primary btn-sm"
              @click="fetchRecords(1)"
            >
              <Icon name="search" size="sm" />
            </button>
            <button
              type="button"
              data-testid="filter-reset-btn"
              class="btn btn-secondary btn-sm"
              @click="resetFilters"
            >
              {{ t('admin.checkin.filters.reset') }}
            </button>
          </div>
        </div>

        <DataTable
          :columns="columns"
          :data="records"
          :loading="loadingRecords"
          row-key="id"
        >
          <template #cell-created_at="{ row }">
            <span class="whitespace-nowrap text-xs text-gray-600 dark:text-gray-300">
              {{ formatDateTime(row.created_at) }}
            </span>
          </template>
          <template #cell-user="{ row }">
            <div class="text-xs">
              <p class="font-medium text-gray-900 dark:text-white">
                {{ row.user_email || row.email || row.username || `#${row.user_id}` }}
              </p>
              <p class="text-gray-400 dark:text-dark-400">ID: {{ row.user_id }}</p>
            </div>
          </template>
          <template #cell-streak_days="{ row }">
            <span class="text-xs font-medium text-gray-700 dark:text-gray-300">
              {{ row.streak_days }} ({{ t('admin.checkin.cycleDay', { day: row.milestone_cycle_day }) }})
            </span>
          </template>
          <template #cell-total_amount="{ row }">
            <div class="text-xs">
              <p class="font-mono font-semibold text-emerald-600 dark:text-emerald-400">
                +${{ formatUsd(row.total_amount) }}
              </p>
              <p class="font-mono text-gray-400 dark:text-dark-400">
                ${{ formatUsd(row.base_amount) }}
                <template v-if="row.milestone_bonus_amount > 0">
                  + ${{ formatUsd(row.milestone_bonus_amount) }}
                </template>
              </p>
            </div>
          </template>
          <template #cell-expires_at="{ row }">
            <span class="whitespace-nowrap text-xs text-gray-600 dark:text-gray-300">
              {{ formatDateTimeToMinute(row.expires_at) }}
            </span>
          </template>
          <template #cell-client_ip="{ row }">
            <span class="font-mono text-xs text-gray-500 dark:text-gray-400">
              {{ row.client_ip || '-' }}
            </span>
          </template>
          <template #empty>
            <EmptyState icon="calendar" :title="t('admin.checkin.empty')" />
          </template>
        </DataTable>

        <Pagination
          v-if="recordsTotal > 0"
          :total="recordsTotal"
          :page="recordsPage"
          :page-size="recordsPageSize"
          @update:page="fetchRecords"
          @update:page-size="handlePageSizeChange"
        />
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import type { Column } from '@/components/common/types'
import { adminAPI } from '@/api/admin'
import { formatDateTime, formatDateTimeToMinute } from '@/utils/format'
import type { AdminCheckinRecord, AdminCheckinStats } from '@/types'

const { t } = useI18n()

const loadingStats = ref(false)
const loadingRecords = ref(false)
const rangeDays = ref(30)
const stats = ref<AdminCheckinStats | null>(null)
const records = ref<AdminCheckinRecord[]>([])
const recordsTotal = ref(0)
const recordsPage = ref(1)
const recordsPageSize = ref(20)

const filters = reactive({
  search: '',
  userId: '',
  clientIp: '',
  startDate: '',
  endDate: ''
})

const rangeOptions = computed(() => [
  { value: 7, label: t('admin.checkin.stats.rangeDays', { days: 7 }) },
  { value: 14, label: t('admin.checkin.stats.rangeDays', { days: 14 }) },
  { value: 30, label: t('admin.checkin.stats.rangeDays', { days: 30 }) },
  { value: 90, label: t('admin.checkin.stats.rangeDays', { days: 90 }) }
])

const columns = computed<Column[]>(() => [
  { key: 'created_at', label: t('admin.checkin.columns.time') },
  { key: 'checkin_date', label: t('admin.checkin.columns.date') },
  { key: 'user', label: t('admin.checkin.columns.user') },
  { key: 'streak_days', label: t('admin.checkin.columns.streak') },
  { key: 'total_amount', label: t('admin.checkin.columns.totalAmount') },
  { key: 'expires_at', label: t('admin.checkin.columns.expiresAt') },
  { key: 'client_ip', label: t('admin.checkin.columns.clientIp') }
])

const budgetBarClass = computed(() => {
  const pct = stats.value?.daily_budget_percent ?? 0
  if (pct >= 90) return 'bg-red-500'
  if (pct >= 70) return 'bg-amber-500'
  return 'bg-emerald-500'
})

function formatUsd(val: number): string {
  const num = Number(val)
  if (!Number.isFinite(num)) return '0.00'
  return num.toFixed(2)
}

function formatPercent(rate: number): string {
  const num = Number(rate)
  if (!Number.isFinite(num)) return '0.0%'
  return `${(num * 100).toFixed(1)}%`
}

async function fetchStats() {
  loadingStats.value = true
  try {
    stats.value = await adminAPI.checkin.getStats(rangeDays.value)
  } catch (error) {
    console.warn('Failed to fetch check-in stats:', error)
    stats.value = null
  } finally {
    loadingStats.value = false
  }
}

async function fetchRecords(page = recordsPage.value) {
  recordsPage.value = page
  loadingRecords.value = true
  try {
    const parsedUserId = filters.userId ? Number(filters.userId) : undefined
    const validUserId =
      parsedUserId != null && Number.isInteger(parsedUserId) && parsedUserId > 0
        ? parsedUserId
        : undefined
    const res = await adminAPI.checkin.listRecords({
      page,
      page_size: recordsPageSize.value,
      user_id: validUserId,
      search: filters.search || undefined,
      client_ip: filters.clientIp || undefined,
      start_date: filters.startDate || undefined,
      end_date: filters.endDate || undefined
    })
    records.value = res.items ?? []
    recordsTotal.value = res.total ?? 0
  } catch (error) {
    console.warn('Failed to fetch check-in records:', error)
    records.value = []
    recordsTotal.value = 0
  } finally {
    loadingRecords.value = false
  }
}

function handleRangeChange() {
  void fetchStats()
}

function handlePageSizeChange(size: number) {
  recordsPageSize.value = size
  void fetchRecords(1)
}

function resetFilters() {
  filters.search = ''
  filters.userId = ''
  filters.clientIp = ''
  filters.startDate = ''
  filters.endDate = ''
  void fetchRecords(1)
}

function refreshAll() {
  void Promise.all([fetchStats(), fetchRecords(recordsPage.value)])
}

onMounted(() => {
  refreshAll()
})
</script>
