<template>
  <AppLayout>
    <div class="space-y-6">
      <section
        class="flex flex-col gap-4 border-b border-gray-200 pb-5 dark:border-dark-700 sm:flex-row sm:items-end sm:justify-between"
      >
        <div class="min-w-0">
          <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
            {{ t('admin.customPlatforms.title') }}
          </h2>
          <p class="mt-1 max-w-3xl text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.customPlatforms.description') }}
          </p>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <div class="relative w-full sm:w-64">
            <input
              v-model.trim="searchQuery"
              data-testid="platform-search"
              type="search"
              class="input w-full"
              :placeholder="t('admin.customPlatforms.searchPlaceholder')"
              autocomplete="off"
            />
          </div>
          <button
            type="button"
            class="btn btn-secondary"
            :disabled="loading"
            :title="t('admin.customPlatforms.refresh')"
            data-testid="refresh-platforms"
            @click="loadPlatforms"
          >
            <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
          </button>
          <button
            type="button"
            class="btn btn-primary"
            data-testid="add-platform"
            @click="openCreate"
          >
            <Icon name="plus" size="sm" />
            {{ t('admin.customPlatforms.addPlatform') }}
          </button>
        </div>
      </section>

      <p
        v-if="loadError"
        data-testid="platforms-load-error"
        class="rounded-xl border border-red-300 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-800/60 dark:bg-red-900/20 dark:text-red-300"
      >
        {{ loadError }}
      </p>

      <!-- Builtin (read-only) -->
      <section class="card overflow-hidden">
        <header class="flex flex-wrap items-center justify-between gap-2 border-b border-gray-200 px-5 py-4 dark:border-dark-700">
          <div>
            <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
              {{ t('admin.customPlatforms.builtinSection.title') }}
            </h3>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">
              {{ t('admin.customPlatforms.builtinSection.description') }}
            </p>
          </div>
          <span class="badge badge-gray">{{ t('admin.customPlatforms.builtinSection.readOnly') }}</span>
        </header>
        <div class="overflow-x-auto">
          <table class="w-full text-sm" data-testid="builtin-platforms-table">
            <thead class="bg-gray-50/80 text-xs uppercase text-gray-500 dark:bg-dark-800/60 dark:text-dark-400">
              <tr>
                <th class="px-5 py-2.5 text-left font-medium">
                  {{ t('admin.customPlatforms.columns.platform') }}
                </th>
                <th class="px-5 py-2.5 text-left font-medium">
                  {{ t('admin.customPlatforms.columns.id') }}
                </th>
                <th class="px-5 py-2.5 text-left font-medium">
                  {{ t('admin.customPlatforms.columns.protocols') }}
                </th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
              <tr
                v-for="spec in filteredBuiltinPlatforms"
                :key="spec.id"
                class="transition-colors hover:bg-gray-50/60 dark:hover:bg-dark-700/40"
                :data-testid="`builtin-platform-${spec.id}`"
              >
                <td class="px-5 py-3">
                  <div class="flex items-center gap-2.5">
                    <span
                      class="flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-lg border"
                      :class="platformBadgeClass(spec.id)"
                    >
                      <PlatformIcon :platform="spec.id" size="md" />
                    </span>
                    <div class="min-w-0">
                      <p class="truncate font-medium text-gray-900 dark:text-white">
                        {{ spec.display_name }}
                      </p>
                      <p class="truncate text-xs text-gray-500 dark:text-dark-400">
                        {{ t(`admin.customPlatforms.gateways.${spec.gateway}`) }}
                      </p>
                    </div>
                  </div>
                </td>
                <td class="px-5 py-3">
                  <code class="rounded bg-gray-100 px-1.5 py-0.5 text-xs text-gray-600 dark:bg-dark-700 dark:text-dark-300">
                    {{ spec.id }}
                  </code>
                </td>
                <td class="px-5 py-3">
                  <div class="flex flex-wrap gap-1">
                    <span
                      v-for="key in builtinProtocolKeys(spec)"
                      :key="key"
                      class="inline-flex items-center rounded-md border px-1.5 py-0.5 text-xs font-medium"
                      :class="platformBadgeClass(spec.id)"
                    >
                      {{ protocolLabel(key) }}
                    </span>
                    <span
                      v-if="!builtinProtocolKeys(spec).length"
                      class="text-xs text-gray-400 dark:text-dark-500"
                    >
                      {{ t('admin.customPlatforms.protocols.empty') }}
                    </span>
                  </div>
                </td>
              </tr>
              <tr v-if="!filteredBuiltinPlatforms.length" data-testid="builtin-platforms-no-match">
                <td colspan="3" class="px-5 py-8 text-center text-xs text-gray-500 dark:text-dark-400">
                  {{ t('admin.customPlatforms.builtinSection.noMatch') }}
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <!-- Custom -->
      <section class="card overflow-hidden">
        <header class="flex flex-wrap items-center justify-between gap-2 border-b border-gray-200 px-5 py-4 dark:border-dark-700">
          <div>
            <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
              {{ t('admin.customPlatforms.customSection.title') }}
            </h3>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">
              {{ t('admin.customPlatforms.customSection.description') }}
            </p>
          </div>
          <span class="badge badge-gray">{{ customPlatforms.length }}</span>
        </header>

        <div
          v-if="!loading && customPlatforms.length === 0"
          class="flex flex-col items-center justify-center gap-1 px-6 py-12 text-center"
          data-testid="custom-platforms-empty"
        >
          <p class="text-sm font-medium text-gray-900 dark:text-white">
            {{ t('admin.customPlatforms.customSection.empty') }}
          </p>
          <p class="text-xs text-gray-500 dark:text-dark-400">
            {{ t('admin.customPlatforms.customSection.emptyHint') }}
          </p>
        </div>

        <div v-else class="overflow-x-auto">
          <table class="w-full text-sm" data-testid="custom-platforms-table">
            <thead class="bg-gray-50/80 text-xs uppercase text-gray-500 dark:bg-dark-800/60 dark:text-dark-400">
              <tr>
                <th class="px-5 py-2.5 text-left font-medium">
                  {{ t('admin.customPlatforms.columns.platform') }}
                </th>
                <th class="px-5 py-2.5 text-left font-medium">
                  {{ t('admin.customPlatforms.columns.id') }}
                </th>
                <th class="px-5 py-2.5 text-left font-medium">
                  {{ t('admin.customPlatforms.columns.protocols') }}
                </th>
                <th class="px-5 py-2.5 text-left font-medium">
                  {{ t('admin.customPlatforms.columns.usage') }}
                </th>
                <th class="px-5 py-2.5 text-left font-medium">
                  {{ t('admin.customPlatforms.columns.status') }}
                </th>
                <th class="px-5 py-2.5 text-right font-medium">
                  {{ t('admin.customPlatforms.columns.actions') }}
                </th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
              <tr
                v-for="platform in filteredCustomPlatforms"
                :key="platform.id"
                class="transition-colors hover:bg-gray-50/60 dark:hover:bg-dark-700/40"
                :data-testid="`custom-platform-row-${platform.id}`"
              >
                <td class="px-5 py-3">
                  <div class="flex items-center gap-2.5">
                    <span
                      class="flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-lg border"
                      :class="presetBadgeClass(platform.color_preset)"
                    >
                      <PlatformIcon :platform="platform.id" size="md" />
                    </span>
                    <div class="min-w-0">
                      <p class="truncate font-medium text-gray-900 dark:text-white">
                        {{ platform.display_name }}
                      </p>
                      <p class="truncate text-xs text-gray-500 dark:text-dark-400">
                        {{ t(`admin.customPlatforms.routing.${platform.multi_protocol?.routing ?? 'by_inbound'}`) }}
                      </p>
                    </div>
                  </div>
                </td>
                <td class="px-5 py-3">
                  <code class="rounded bg-gray-100 px-1.5 py-0.5 text-xs text-gray-600 dark:bg-dark-700 dark:text-dark-300">
                    {{ platform.id }}
                  </code>
                </td>
                <td class="px-5 py-3">
                  <div class="flex flex-wrap gap-1">
                    <span
                      v-for="key in platformProtocolKeys(platform)"
                      :key="key"
                      class="inline-flex items-center rounded-md border px-1.5 py-0.5 text-xs font-medium"
                      :class="presetBadgeClass(platform.color_preset)"
                    >
                      {{ protocolLabel(key) }}
                    </span>
                    <span
                      v-if="!platformProtocolKeys(platform).length"
                      class="text-xs text-gray-400 dark:text-dark-500"
                    >
                      {{ t('admin.customPlatforms.protocols.empty') }}
                    </span>
                  </div>
                </td>
                <td class="px-5 py-3">
                  <div class="flex flex-wrap gap-x-3 gap-y-1 text-xs text-gray-600 dark:text-dark-300">
                    <span
                      v-for="item in usageItems(platform)"
                      :key="item.key"
                      :class="item.count > 0 ? 'font-medium text-gray-900 dark:text-white' : 'text-gray-400 dark:text-dark-500'"
                    >
                      {{ item.label }} {{ item.count }}
                    </span>
                  </div>
                </td>
                <td class="px-5 py-3">
                  <div class="flex items-center gap-2">
                    <Toggle
                      :model-value="platform.enabled"
                      :data-testid="`toggle-platform-${platform.id}`"
                      :disabled="togglingId === platform.id"
                      @update:model-value="value => toggleEnabled(platform, value)"
                    />
                    <span class="text-xs text-gray-500 dark:text-dark-400">
                      {{
                        platform.enabled
                          ? t('admin.customPlatforms.status.enabled')
                          : t('admin.customPlatforms.status.disabled')
                      }}
                    </span>
                  </div>
                </td>
                <td class="px-5 py-3">
                  <div class="flex items-center justify-end gap-1">
                    <button
                      type="button"
                      class="rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-700 dark:hover:bg-dark-600 dark:hover:text-gray-300"
                      :title="t('admin.customPlatforms.actions.edit')"
                      :data-testid="`edit-platform-${platform.id}`"
                      @click="openEdit(platform)"
                    >
                      <Icon name="edit" size="sm" />
                    </button>
                    <button
                      type="button"
                      class="rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20 dark:hover:text-red-400"
                      :title="t('admin.customPlatforms.actions.delete')"
                      :data-testid="`delete-platform-${platform.id}`"
                      @click="openDelete(platform)"
                    >
                      <Icon name="trash" size="sm" />
                    </button>
                  </div>
                </td>
              </tr>
              <tr v-if="!loading && !filteredCustomPlatforms.length" data-testid="custom-platforms-no-match">
                <td colspan="6" class="px-5 py-8 text-center text-xs text-gray-500 dark:text-dark-400">
                  {{ t('admin.customPlatforms.customSection.noMatch') }}
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </div>

    <CustomPlatformModal
      :show="showModal"
      :platform="editingPlatform"
      :builtin-ids="builtinPlatformIds"
      :custom-ids="customPlatformIds"
      @close="closeModal"
      @saved="handleSaved"
    />

    <BaseDialog
      :show="!!deleteTarget"
      :title="t('admin.customPlatforms.deleteDialog.title')"
      width="normal"
      @close="closeDelete"
    >
      <div v-if="deleteTarget" class="space-y-4 text-sm">
        <template v-if="!deleteInUse">
          <p class="text-gray-700 dark:text-gray-300">
            {{
              t('admin.customPlatforms.deleteDialog.message', {
                name: deleteTarget.display_name
              })
            }}
          </p>
          <p
            v-if="deleteError"
            data-testid="delete-platform-error"
            class="input-error-text"
          >
            {{ deleteError }}
          </p>
        </template>
        <template v-else>
          <p class="font-medium text-amber-700 dark:text-amber-300">
            {{ t('admin.customPlatforms.deleteDialog.inUseTitle') }}
          </p>
          <p class="text-gray-600 dark:text-dark-300">
            {{ t('admin.customPlatforms.deleteDialog.inUseHint') }}
          </p>
          <ul
            data-testid="usage-breakdown"
            class="grid grid-cols-2 gap-2 rounded-xl border border-amber-200 bg-amber-50 p-3 text-xs dark:border-amber-800/60 dark:bg-amber-900/20"
          >
            <li
              v-for="item in deleteUsageItems"
              :key="item.key"
              class="flex items-center justify-between gap-2 text-amber-800 dark:text-amber-200"
            >
              <span>{{ item.label }}</span>
              <span class="font-semibold" :data-testid="`usage-${item.key}`">{{ item.count }}</span>
            </li>
          </ul>
        </template>
      </div>

      <template #footer>
        <div class="flex justify-end gap-3">
          <button type="button" class="btn btn-secondary" @click="closeDelete">
            {{ t('admin.customPlatforms.deleteDialog.close') }}
          </button>
          <button
            v-if="!deleteInUse"
            type="button"
            class="btn btn-primary !bg-red-600 hover:!bg-red-700"
            data-testid="confirm-delete-platform"
            :disabled="deleting"
            @click="confirmDelete"
          >
            {{ t('admin.customPlatforms.deleteDialog.confirm') }}
          </button>
        </div>
      </template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Toggle from '@/components/common/Toggle.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import Icon from '@/components/icons/Icon.vue'
import CustomPlatformModal from '@/components/admin/channel/CustomPlatformModal.vue'
import {
  buildCustomPlatformPayload,
  formFromCustomPlatform,
  platformProtocolKeys,
  presetBadgeClass,
  type CustomPlatformFormState
} from '@/components/admin/channel/customPlatformForm'
import {
  deletePlatform,
  isCustomPlatformInUseError,
  list,
  normalizeUsageCounts,
  update,
  EMPTY_USAGE_COUNTS,
  type CustomPlatformAdmin,
  type CustomPlatformUsageCounts
} from '@/api/admin/customPlatforms'
import {
  listPlatforms,
  type PlatformSpec
} from '@/constants/platformCatalog'
import { loadPlatformCatalog } from '@/constants/platformCatalogLoader'
import { platformBadgeClass } from '@/utils/platformColors'

const { t } = useI18n()

const builtinPlatforms = ref<PlatformSpec[]>(
  listPlatforms().filter(spec => spec.custom !== true)
)
const customPlatforms = ref<CustomPlatformAdmin[]>([])
const searchQuery = ref('')
const loading = ref(false)
const loadError = ref('')

const showModal = ref(false)
const editingPlatform = ref<CustomPlatformAdmin | null>(null)
const togglingId = ref('')

const deleteTarget = ref<CustomPlatformAdmin | null>(null)
const deleteInUse = ref<CustomPlatformUsageCounts | null>(null)
const deleteError = ref('')
const deleting = ref(false)

const builtinPlatformIds = computed(() => builtinPlatforms.value.map(spec => spec.id))
const customPlatformIds = computed(() => customPlatforms.value.map(platform => platform.id))
// 管理端列表的内置平台实体不带 multi_protocol；协议徽章回落到前端内置清单
// （与后端同一份注册表），避免只显示网关对应的单一协议。
const localPlatformSpecs = computed(
  () => new Map(listPlatforms().map(spec => [spec.id, spec]))
)
const sortedCustomPlatforms = computed(() =>
  [...customPlatforms.value].sort(
    (a, b) => (a.sort_order ?? 0) - (b.sort_order ?? 0) || a.id.localeCompare(b.id)
  )
)

function matchesSearch(item: { id: string; display_name: string }): boolean {
  const query = searchQuery.value.trim().toLowerCase()
  if (!query) return true
  return (
    item.id.toLowerCase().includes(query) ||
    item.display_name.toLowerCase().includes(query)
  )
}

const filteredBuiltinPlatforms = computed(() =>
  builtinPlatforms.value.filter(matchesSearch)
)
const filteredCustomPlatforms = computed(() =>
  sortedCustomPlatforms.value.filter(matchesSearch)
)

const deleteUsageItems = computed(() => usageItemsFromCounts(deleteInUse.value))

function protocolLabel(key: string): string {
  const known = ['chat_completions', 'responses', 'anthropic', 'embeddings', 'images']
  if (!known.includes(key)) return key
  return t(`admin.customPlatforms.protocols.${key}`)
}

function builtinProtocolKeys(spec: PlatformSpec): string[] {
  return platformProtocolKeys(localPlatformSpecs.value.get(spec.id) ?? spec)
}

function usageItems(platform: CustomPlatformAdmin) {
  return usageItemsFromCounts(platform.usage_counts)
}

function usageItemsFromCounts(counts: CustomPlatformUsageCounts | null | undefined) {
  const normalized = normalizeUsageCounts(counts ?? EMPTY_USAGE_COUNTS)
  return [
    {
      key: 'accounts',
      label: t('admin.customPlatforms.usage.accounts'),
      count: normalized.accounts
    },
    {
      key: 'groups',
      label: t('admin.customPlatforms.usage.groups'),
      count: normalized.groups
    },
    {
      key: 'composite_routes',
      label: t('admin.customPlatforms.usage.compositeRoutes'),
      count: normalized.composite_routes
    },
    {
      key: 'user_quotas',
      label: t('admin.customPlatforms.usage.userQuotas'),
      count: normalized.user_quotas
    }
  ]
}

function errorMessage(error: unknown, fallback: string): string {
  if (typeof error === 'object' && error !== null && 'message' in error) {
    const message = String((error as { message?: unknown }).message ?? '')
    if (message) return message
  }
  return fallback
}

async function loadPlatforms() {
  loading.value = true
  loadError.value = ''
  try {
    const result = await list()
    builtinPlatforms.value = result.builtin_platforms
    customPlatforms.value = result.custom_platforms
  } catch (error) {
    loadError.value = errorMessage(error, t('admin.customPlatforms.loadFailed'))
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editingPlatform.value = null
  showModal.value = true
}

function openEdit(platform: CustomPlatformAdmin) {
  editingPlatform.value = platform
  showModal.value = true
}

function closeModal() {
  showModal.value = false
  editingPlatform.value = null
}

async function handleSaved() {
  closeModal()
  await Promise.all([loadPlatforms(), loadPlatformCatalog()])
}

function payloadFromPlatform(platform: CustomPlatformAdmin, overrides: Partial<CustomPlatformFormState> = {}) {
  const form: CustomPlatformFormState = { ...formFromCustomPlatform(platform), ...overrides }
  return buildCustomPlatformPayload(form)
}

async function toggleEnabled(platform: CustomPlatformAdmin, enabled: boolean) {
  if (togglingId.value) return
  togglingId.value = platform.id
  const previous = customPlatforms.value
  customPlatforms.value = customPlatforms.value.map(item =>
    item.id === platform.id ? { ...item, enabled } : item
  )
  try {
    const saved = await update(platform.id, payloadFromPlatform(platform, { enabled }))
    customPlatforms.value = customPlatforms.value.map(item =>
      item.id === platform.id ? { ...item, ...saved, usage_counts: item.usage_counts } : item
    )
    await loadPlatformCatalog()
  } catch (error) {
    customPlatforms.value = previous
    loadError.value = errorMessage(error, t('admin.customPlatforms.errors.saveFailed'))
  } finally {
    togglingId.value = ''
  }
}

function openDelete(platform: CustomPlatformAdmin) {
  deleteTarget.value = platform
  deleteInUse.value = null
  deleteError.value = ''
}

function closeDelete() {
  if (deleting.value) return
  deleteTarget.value = null
  deleteInUse.value = null
  deleteError.value = ''
}

async function confirmDelete() {
  const target = deleteTarget.value
  if (!target) return
  deleting.value = true
  deleteError.value = ''
  try {
    await deletePlatform(target.id)
    deleteTarget.value = null
    deleteInUse.value = null
    await Promise.all([loadPlatforms(), loadPlatformCatalog()])
  } catch (error) {
    if (isCustomPlatformInUseError(error)) {
      deleteInUse.value = error.usage_counts
    } else if (typeof error === 'object' && error !== null && (error as { status?: number }).status === 409) {
      // 拦截器可能丢弃了 409 响应体：回查列表拿实时引用计数。
      await loadPlatforms()
      const refreshed = customPlatforms.value.find(item => item.id === target.id)
      deleteInUse.value = normalizeUsageCounts(
        refreshed?.usage_counts ?? EMPTY_USAGE_COUNTS
      )
    } else {
      deleteError.value = errorMessage(error, t('admin.customPlatforms.errors.saveFailed'))
    }
  } finally {
    deleting.value = false
  }
}

onMounted(loadPlatforms)
</script>
