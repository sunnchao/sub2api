<template>
  <div class="space-y-4">
    <!-- Toolbar -->
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div class="flex flex-1 flex-wrap items-center gap-3">
        <div class="w-full sm:w-56">
          <input
            v-model="filters.search"
            type="text"
            class="input"
            :placeholder="t('admin.subscriptions.templates.searchPlaceholder')"
            @input="debouncedLoad"
          />
        </div>
        <div class="w-full sm:w-44">
          <Select
            v-model="filters.group_id"
            :options="groupFilterOptions"
            :placeholder="t('admin.subscriptions.allGroups')"
            @change="loadTemplates"
          />
        </div>
        <div class="w-full sm:w-36">
          <Select
            v-model="filters.status"
            :options="statusFilterOptions"
            :placeholder="t('admin.subscriptions.allStatus')"
            @change="loadTemplates"
          />
        </div>
      </div>

      <div class="flex items-center gap-2">
        <button
          type="button"
          class="btn btn-secondary"
          :disabled="loading"
          :title="t('common.refresh')"
          @click="loadTemplates"
        >
          <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
        </button>
        <button
          type="button"
          class="btn btn-primary"
          data-testid="create-template-btn"
          @click="openEdit(null)"
        >
          <Icon name="plus" size="md" class="mr-2" />
          {{ t('admin.subscriptions.templates.create') }}
        </button>
      </div>
    </div>

    <!-- Templates Table -->
    <DataTable :columns="columns" :data="templates" :loading="loading" row-key="id">
      <template #cell-name="{ row }">
        <div>
          <div class="font-medium text-gray-900 dark:text-white">{{ row.name }}</div>
          <div v-if="row.description" class="text-xs text-gray-500 dark:text-gray-400">
            {{ row.description }}
          </div>
        </div>
      </template>

      <template #cell-group="{ row }">
        <GroupBadge
          v-if="row.group"
          :name="row.group.name"
          :platform="row.group.platform"
          :subscription-type="row.group.subscription_type"
          :rate-multiplier="row.group.rate_multiplier"
        />
        <span v-else class="text-sm text-gray-400">#{{ row.group_id }}</span>
      </template>

      <template #cell-limits="{ row }">
        <div class="flex flex-wrap gap-1.5 text-xs">
          <span v-if="row.hourly_limit_usd != null" class="badge badge-gray">
            {{ t('admin.subscriptions.hourly', { hours: row.hourly_window_hours || 5 }) }}: ${{ row.hourly_limit_usd }}
          </span>
          <span v-if="row.daily_limit_usd != null" class="badge badge-gray">
            {{ t('admin.subscriptions.daily') }}: ${{ row.daily_limit_usd }}
          </span>
          <span v-if="row.weekly_limit_usd != null" class="badge badge-gray">
            {{ t('admin.subscriptions.weekly') }}: ${{ row.weekly_limit_usd }}
          </span>
          <span v-if="row.monthly_limit_usd != null" class="badge badge-gray">
            {{ t('admin.subscriptions.monthly') }}: ${{ row.monthly_limit_usd }}
          </span>
          <span
            v-if="
              row.hourly_limit_usd == null &&
              row.daily_limit_usd == null &&
              row.weekly_limit_usd == null &&
              row.monthly_limit_usd == null
            "
            class="badge badge-success"
          >
            ∞ {{ t('admin.subscriptions.unlimited') }}
          </span>
        </div>
      </template>

      <template #cell-default_validity_days="{ value }">
        <span class="text-sm text-gray-700 dark:text-gray-300">
          {{ value }} {{ t('admin.redeem.days') }}
        </span>
      </template>

      <template #cell-status="{ value }">
        <span :class="['badge', value === 'active' ? 'badge-success' : 'badge-gray']">
          {{ value === 'active' ? t('admin.subscriptions.status.active') : t('admin.subscriptions.templates.inactive') }}
        </span>
      </template>

      <template #cell-actions="{ row }">
        <div class="flex items-center gap-1">
          <button
            type="button"
            class="rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-blue-50 hover:text-blue-600 dark:hover:bg-blue-900/20 dark:hover:text-blue-400"
            :title="t('common.edit')"
            :data-testid="`edit-template-${row.id}`"
            @click="openEdit(row)"
          >
            <Icon name="edit" size="sm" />
          </button>
          <button
            type="button"
            class="rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20 dark:hover:text-red-400"
            :title="t('common.delete')"
            :data-testid="`delete-template-${row.id}`"
            @click="confirmDelete(row)"
          >
            <Icon name="trash" size="sm" />
          </button>
        </div>
      </template>
    </DataTable>

    <SubscriptionTemplateDialog
      :show="showEditDialog"
      :template="editingTemplate"
      :groups="groups"
      @close="showEditDialog = false"
      @saved="handleTemplateSaved"
    />

    <ConfirmDialog
      :show="showDeleteDialog"
      :title="t('admin.subscriptions.templates.deleteTitle')"
      :message="t('admin.subscriptions.templates.deleteConfirm', { name: deletingTemplate?.name || '' })"
      :confirm-text="t('common.delete')"
      danger
      @confirm="handleDelete"
      @cancel="showDeleteDialog = false"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { subscriptionTemplatesAPI, type SubscriptionTemplate } from '@/api/admin/subscriptionTemplates'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { Group } from '@/types'
import type { Column } from '@/components/common/types'
import DataTable from '@/components/common/DataTable.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import GroupBadge from '@/components/common/GroupBadge.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import SubscriptionTemplateDialog from './SubscriptionTemplateDialog.vue'

const props = defineProps<{
  groups: Group[]
}>()

const emit = defineEmits<{
  changed: []
}>()

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(false)
const templates = ref<SubscriptionTemplate[]>([])
const showEditDialog = ref(false)
const editingTemplate = ref<SubscriptionTemplate | null>(null)
const showDeleteDialog = ref(false)
const deletingTemplate = ref<SubscriptionTemplate | null>(null)

const filters = reactive({
  search: '',
  group_id: '',
  status: ''
})

let searchTimer: ReturnType<typeof setTimeout> | null = null

const columns = computed<Column[]>(() => [
  { key: 'name', label: t('admin.subscriptions.templates.name') },
  { key: 'group', label: t('admin.subscriptions.templates.boundGroup') },
  { key: 'limits', label: t('admin.subscriptions.templates.limits') },
  { key: 'default_validity_days', label: t('admin.subscriptions.templates.defaultValidityDays') },
  { key: 'status', label: t('admin.subscriptions.templates.status') },
  { key: 'actions', label: t('common.actions') }
])

const groupFilterOptions = computed(() => [
  { value: '', label: t('admin.subscriptions.allGroups') },
  ...props.groups.map((g) => ({ value: String(g.id), label: g.name }))
])

const statusFilterOptions = computed(() => [
  { value: '', label: t('admin.subscriptions.allStatus') },
  { value: 'active', label: t('admin.subscriptions.status.active') },
  { value: 'inactive', label: t('admin.subscriptions.templates.inactive') }
])

async function loadTemplates() {
  loading.value = true
  try {
    templates.value = await subscriptionTemplatesAPI.list({
      group_id: filters.group_id ? Number(filters.group_id) : undefined,
      status: (filters.status as 'active' | 'inactive' | '') || undefined,
      search: filters.search || undefined
    })
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    loading.value = false
  }
}

function debouncedLoad() {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(loadTemplates, 250)
}

function openEdit(tpl: SubscriptionTemplate | null) {
  editingTemplate.value = tpl
  showEditDialog.value = true
}

function handleTemplateSaved() {
  loadTemplates()
  emit('changed')
}

function confirmDelete(tpl: SubscriptionTemplate) {
  deletingTemplate.value = tpl
  showDeleteDialog.value = true
}

async function handleDelete() {
  if (!deletingTemplate.value) return
  try {
    await subscriptionTemplatesAPI.remove(deletingTemplate.value.id)
    appStore.showSuccess(t('common.deleted'))
    showDeleteDialog.value = false
    deletingTemplate.value = null
    loadTemplates()
    emit('changed')
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  }
}

onMounted(() => {
  loadTemplates()
})

defineExpose({ loadTemplates })
</script>
