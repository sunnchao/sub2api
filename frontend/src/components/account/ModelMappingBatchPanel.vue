<template>
  <div class="mb-3" data-testid="model-mapping-batch-panel">
    <!-- Compact Trigger Bar -->
    <div class="flex flex-wrap items-center gap-2">
      <button
        v-if="canSyncUpstream && showUpstreamSync"
        type="button"
        data-testid="mapping-batch-open-upstream"
        :disabled="isSyncingUpstream"
        @click="openPanel('upstream')"
        :class="[
          'inline-flex items-center gap-1.5 rounded-lg border px-2.5 py-1.5 text-xs font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-60',
          isOpen && activeSource === 'upstream'
            ? 'border-emerald-500 bg-emerald-50 text-emerald-700 dark:border-emerald-500 dark:bg-emerald-900/30 dark:text-emerald-300'
            : 'border-emerald-200 text-emerald-700 hover:bg-emerald-50 dark:border-emerald-800 dark:text-emerald-400 dark:hover:bg-emerald-900/30'
        ]"
      >
        <Icon name="refresh" size="xs" :class="isSyncingUpstream ? 'animate-spin' : ''" />
        {{
          isSyncingUpstream
            ? t('admin.accounts.syncUpstreamModelsLoading')
            : t('admin.accounts.mappingBatch.fetchUpstreamAndMap')
        }}
      </button>

      <button
        v-if="allowWhitelistSource && allowedModels.length > 0"
        type="button"
        data-testid="mapping-batch-open-whitelist"
        @click="openPanel('whitelist')"
        :class="[
          'inline-flex items-center gap-1.5 rounded-lg border px-2.5 py-1.5 text-xs font-medium transition-colors',
          isOpen && activeSource === 'whitelist'
            ? 'border-blue-500 bg-blue-50 text-blue-700 dark:border-blue-500 dark:bg-blue-900/30 dark:text-blue-300'
            : 'border-blue-200 text-blue-700 hover:bg-blue-50 dark:border-blue-800 dark:text-blue-400 dark:hover:bg-blue-900/30'
        ]"
      >
        {{ t('admin.accounts.mappingBatch.convertWhitelist', { count: allowedModels.length }) }}
      </button>

      <button
        v-if="modelMappings.length > 0"
        type="button"
        data-testid="mapping-batch-open-existing"
        @click="openPanel('existing')"
        :class="[
          'inline-flex items-center gap-1.5 rounded-lg border px-2.5 py-1.5 text-xs font-medium transition-colors',
          isOpen && activeSource === 'existing'
            ? 'border-purple-500 bg-purple-50 text-purple-700 dark:border-purple-500 dark:bg-purple-900/30 dark:text-purple-300'
            : 'border-purple-200 text-purple-700 hover:bg-purple-50 dark:border-purple-800 dark:text-purple-400 dark:hover:bg-purple-900/30'
        ]"
      >
        {{ t('admin.accounts.mappingBatch.regexBatchReplace') }}
      </button>

      <button
        v-if="undoSnapshot"
        type="button"
        data-testid="mapping-batch-undo"
        @click="undoLastApply"
        class="inline-flex items-center gap-1 rounded-lg border border-amber-200 px-2.5 py-1.5 text-xs font-medium text-amber-700 transition-colors hover:bg-amber-50 dark:border-amber-800 dark:text-amber-300 dark:hover:bg-amber-900/30"
      >
        {{ t('admin.accounts.mappingBatch.undoLastApply') }}
      </button>
    </div>

    <!-- Inline Expandable Workspace -->
    <div
      v-if="isOpen"
      data-testid="mapping-batch-workspace"
      class="mt-2.5 rounded-xl border border-gray-200 bg-gray-50/80 p-3 dark:border-dark-600 dark:bg-dark-800/70"
    >
      <!-- Workspace Header: Source Switcher & Actions -->
      <div class="mb-2.5 flex flex-wrap items-center justify-between gap-2 border-b border-gray-200 pb-2 dark:border-dark-600">
        <div class="flex flex-wrap items-center gap-1.5">
          <button
            v-if="canSyncUpstream && showUpstreamSync"
            type="button"
            @click="switchSource('upstream')"
            :class="sourceTabClass('upstream')"
          >
            {{ t('admin.accounts.mappingBatch.sourceUpstream') }}
            <span v-if="upstreamModels.length > 0" class="ml-1 opacity-75">({{ upstreamModels.length }})</span>
          </button>
          <button
            v-if="allowWhitelistSource && allowedModels.length > 0"
            type="button"
            @click="switchSource('whitelist')"
            :class="sourceTabClass('whitelist')"
          >
            {{ t('admin.accounts.mappingBatch.sourceWhitelist') }}
            <span class="ml-1 opacity-75">({{ allowedModels.length }})</span>
          </button>
          <button
            v-if="modelMappings.length > 0"
            type="button"
            @click="switchSource('existing')"
            :class="sourceTabClass('existing')"
          >
            {{ t('admin.accounts.mappingBatch.sourceExisting') }}
            <span class="ml-1 opacity-75">({{ modelMappings.length }})</span>
          </button>
        </div>

        <div class="flex items-center gap-1.5">
          <button
            v-if="activeSource === 'upstream' && canSyncUpstream"
            type="button"
            data-testid="mapping-batch-refresh-upstream"
            :disabled="isSyncingUpstream"
            @click="fetchUpstreamModels(true)"
            class="inline-flex items-center gap-1 rounded-md border border-gray-200 bg-white px-2 py-1 text-xs text-gray-600 hover:bg-gray-100 disabled:opacity-60 dark:border-dark-500 dark:bg-dark-700 dark:text-gray-300 dark:hover:bg-dark-600"
          >
            <Icon name="refresh" size="xs" :class="isSyncingUpstream ? 'animate-spin' : ''" />
            {{ t('admin.accounts.mappingBatch.refreshUpstream') }}
          </button>
          <button
            type="button"
            data-testid="mapping-batch-close"
            @click="isOpen = false"
            class="rounded-md p-1 text-gray-400 hover:bg-gray-200 hover:text-gray-600 dark:hover:bg-dark-600 dark:hover:text-gray-200"
            :aria-label="t('common.close')"
          >
            <Icon name="x" size="xs" />
          </button>
        </div>
      </div>

      <!-- Regex Replace Controls -->
      <div class="mb-2.5 space-y-2">
        <div class="grid grid-cols-1 gap-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto]">
          <div>
            <label class="mb-1 block text-[11px] font-medium text-gray-600 dark:text-gray-400">
              {{ t('admin.accounts.mappingBatch.findRegex') }}
            </label>
            <input
              v-model="pattern"
              data-testid="mapping-batch-pattern"
              type="text"
              class="input w-full font-mono text-xs"
              :placeholder="t('admin.accounts.mappingBatch.findRegexPlaceholder')"
            />
          </div>
          <div>
            <label class="mb-1 block text-[11px] font-medium text-gray-600 dark:text-gray-400">
              {{ t('admin.accounts.mappingBatch.replaceWith') }}
            </label>
            <input
              v-model="replacement"
              data-testid="mapping-batch-replacement"
              type="text"
              class="input w-full font-mono text-xs"
              :placeholder="t('admin.accounts.mappingBatch.replaceWithPlaceholder')"
            />
          </div>
          <div class="flex items-end gap-1">
            <button
              type="button"
              data-testid="mapping-batch-flag-g"
              :aria-pressed="flagGlobal"
              @click="flagGlobal = !flagGlobal"
              :title="t('admin.accounts.mappingBatch.flagGlobalTitle')"
              :class="flagButtonClass(flagGlobal)"
            >
              g
            </button>
            <button
              type="button"
              data-testid="mapping-batch-flag-i"
              :aria-pressed="flagIgnoreCase"
              @click="flagIgnoreCase = !flagIgnoreCase"
              :title="t('admin.accounts.mappingBatch.flagIgnoreCaseTitle')"
              :class="flagButtonClass(flagIgnoreCase)"
            >
              i
            </button>
          </div>
        </div>

        <p
          v-if="preview.regexError"
          data-testid="mapping-batch-regex-error"
          class="text-xs text-red-600 dark:text-red-400"
        >
          {{ t('admin.accounts.mappingBatch.invalidRegex', { message: preview.regexError }) }}
        </p>

        <!-- Target Column & Quick Presets -->
        <div class="flex flex-wrap items-center justify-between gap-2">
          <div class="flex items-center gap-1">
            <span class="text-[11px] text-gray-500 dark:text-gray-400">
              {{ t('admin.accounts.mappingBatch.targetColumn') }}
            </span>
            <button
              type="button"
              data-testid="mapping-batch-target-from"
              @click="targetField = 'from'"
              :class="pillClass(targetField === 'from')"
            >
              {{ t('admin.accounts.mappingBatch.targetFrom') }}
            </button>
            <button
              type="button"
              data-testid="mapping-batch-target-to"
              @click="targetField = 'to'"
              :class="pillClass(targetField === 'to')"
            >
              {{ t('admin.accounts.mappingBatch.targetTo') }}
            </button>
          </div>

          <div class="flex flex-wrap items-center gap-1">
            <button
              v-for="preset in presets"
              :key="preset.id"
              type="button"
              :data-testid="`mapping-batch-preset-${preset.id}`"
              @click="applyPreset(preset)"
              class="rounded-md border border-gray-200 bg-white px-2 py-0.5 text-[11px] text-gray-600 transition-colors hover:border-purple-300 hover:bg-purple-50 hover:text-purple-700 dark:border-dark-500 dark:bg-dark-700 dark:text-gray-300 dark:hover:border-purple-700 dark:hover:bg-purple-900/30 dark:hover:text-purple-300"
            >
              {{ t(preset.labelKey) }}
            </button>
          </div>
        </div>
      </div>

      <!-- Filter & Selection Toolbar -->
      <div class="mb-2 flex flex-wrap items-center gap-1.5">
        <div class="relative flex min-w-[180px] flex-1 items-center gap-1">
          <input
            v-model="filterQuery"
            data-testid="mapping-batch-filter"
            type="text"
            class="input w-full py-1 text-xs"
            :placeholder="t('admin.accounts.mappingBatch.filterPlaceholder')"
          />
          <button
            type="button"
            data-testid="mapping-batch-filter-regex"
            :aria-pressed="filterUseRegex"
            @click="filterUseRegex = !filterUseRegex"
            :title="t('admin.accounts.mappingBatch.filterRegexTitle')"
            :class="flagButtonClass(filterUseRegex)"
          >
            .*
          </button>
        </div>

        <button
          type="button"
          data-testid="mapping-batch-select-visible"
          @click="selectVisibleRows"
          class="rounded-md border border-gray-200 bg-white px-2 py-1 text-[11px] text-gray-600 hover:bg-gray-100 dark:border-dark-500 dark:bg-dark-700 dark:text-gray-300 dark:hover:bg-dark-600"
        >
          {{
            filterQuery.trim()
              ? t('admin.accounts.mappingBatch.selectFilteredOnly', { count: visibleRows.length })
              : t('admin.accounts.mappingBatch.selectAll')
          }}
        </button>
        <button
          v-if="pattern.length > 0 && !preview.regexError"
          type="button"
          data-testid="mapping-batch-select-matched"
          @click="selectMatchedRows"
          class="rounded-md border border-gray-200 bg-white px-2 py-1 text-[11px] text-gray-600 hover:bg-gray-100 dark:border-dark-500 dark:bg-dark-700 dark:text-gray-300 dark:hover:bg-dark-600"
        >
          {{ t('admin.accounts.mappingBatch.selectMatchedOnly') }}
        </button>
        <button
          type="button"
          data-testid="mapping-batch-clear-selection"
          @click="clearSelection"
          class="rounded-md border border-gray-200 bg-white px-2 py-1 text-[11px] text-gray-600 hover:bg-gray-100 dark:border-dark-500 dark:bg-dark-700 dark:text-gray-300 dark:hover:bg-dark-600"
        >
          {{ t('admin.accounts.mappingBatch.clearSelection') }}
        </button>
      </div>

      <p v-if="filterError" class="mb-2 text-xs text-red-600 dark:text-red-400">
        {{ t('admin.accounts.mappingBatch.invalidFilterRegex', { message: filterError }) }}
      </p>

      <!-- Conflict Policy (only when importing from upstream/whitelist and conflicts exist) -->
      <div
        v-if="activeSource !== 'existing' && preview.conflictSelectedCount > 0"
        data-testid="mapping-batch-conflict-bar"
        class="mb-2 flex flex-wrap items-center justify-between gap-2 rounded-lg border border-amber-200 bg-amber-50/80 px-2.5 py-1.5 text-xs text-amber-800 dark:border-amber-800/70 dark:bg-amber-900/20 dark:text-amber-300"
      >
        <span>
          {{ t('admin.accounts.mappingBatch.conflictNotice', { count: preview.conflictSelectedCount }) }}
        </span>
        <div class="flex items-center gap-1">
          <button
            type="button"
            data-testid="mapping-batch-conflict-skip"
            @click="conflictPolicy = 'skip'"
            :class="pillClass(conflictPolicy === 'skip')"
          >
            {{ t('admin.accounts.mappingBatch.conflictSkip') }}
          </button>
          <button
            type="button"
            data-testid="mapping-batch-conflict-overwrite"
            @click="conflictPolicy = 'overwrite'"
            :class="pillClass(conflictPolicy === 'overwrite')"
          >
            {{ t('admin.accounts.mappingBatch.conflictOverwrite') }}
          </button>
        </div>
      </div>

      <!-- Scrollable Live Preview Table -->
      <div class="max-h-64 overflow-y-auto rounded-lg border border-gray-200 bg-white dark:border-dark-600 dark:bg-dark-900/60">
        <div
          v-if="isSyncingUpstream && activeSource === 'upstream' && allSourceRows.length === 0"
          class="px-3 py-6 text-center text-xs text-gray-500 dark:text-gray-400"
        >
          {{ t('admin.accounts.syncUpstreamModelsLoading') }}
        </div>
        <div
          v-else-if="preview.rows.length === 0"
          class="px-3 py-6 text-center text-xs text-gray-500 dark:text-gray-400"
        >
          {{ t('admin.accounts.mappingBatch.emptyPreview') }}
        </div>
        <div v-else class="divide-y divide-gray-100 dark:divide-dark-700">
          <label
            v-for="row in preview.rows"
            :key="row.id"
            data-testid="mapping-batch-preview-row"
            :class="[
              'flex cursor-pointer items-start gap-2 px-2.5 py-1.5 text-xs transition-colors hover:bg-gray-50 dark:hover:bg-dark-800/70',
              row.selected && row.error ? 'bg-red-50/70 dark:bg-red-950/30' : '',
              !row.selected ? 'opacity-55' : ''
            ]"
          >
            <input
              type="checkbox"
              :checked="row.selected"
              @change="toggleRow(row.id)"
              class="mt-0.5 rounded border-gray-300 text-purple-600 focus:ring-purple-500 dark:border-dark-500"
            />
            <div class="min-w-0 flex-1">
              <div class="flex flex-wrap items-center gap-1.5 font-mono">
                <span
                  :class="[
                    'break-all',
                    row.changed && row.nextFrom !== row.originalFrom.trim()
                      ? 'font-semibold text-purple-700 dark:text-purple-300'
                      : 'text-gray-800 dark:text-gray-200'
                  ]"
                >
                  {{ row.nextFrom || '∅' }}
                </span>
                <span class="text-gray-400">→</span>
                <span
                  :class="[
                    'break-all',
                    row.changed && row.nextTo !== row.originalTo.trim()
                      ? 'font-semibold text-emerald-700 dark:text-emerald-300'
                      : 'text-gray-600 dark:text-gray-300'
                  ]"
                >
                  {{ row.nextTo || '∅' }}
                </span>
              </div>
              <div
                v-if="row.changed"
                class="mt-0.5 truncate font-mono text-[11px] text-gray-400 line-through"
              >
                {{ row.originalFrom }} → {{ row.originalTo }}
              </div>
            </div>
            <div class="flex shrink-0 flex-wrap items-center gap-1">
              <span
                v-if="row.selected && row.error"
                data-testid="mapping-batch-row-error"
                class="rounded bg-red-100 px-1.5 py-0.5 text-[10px] font-medium text-red-700 dark:bg-red-900/50 dark:text-red-300"
              >
                {{ errorLabel(row.error) }}
              </span>
              <span
                v-else-if="row.selected && row.conflictWithExisting"
                class="rounded bg-amber-100 px-1.5 py-0.5 text-[10px] font-medium text-amber-700 dark:bg-amber-900/40 dark:text-amber-300"
              >
                {{
                  conflictPolicy === 'overwrite'
                    ? t('admin.accounts.mappingBatch.badgeOverwrite')
                    : t('admin.accounts.mappingBatch.badgeSkip')
                }}
              </span>
              <span
                v-else-if="row.selected && row.changed"
                class="rounded bg-emerald-100 px-1.5 py-0.5 text-[10px] font-medium text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300"
              >
                {{ t('admin.accounts.mappingBatch.badgeChanged') }}
              </span>
            </div>
          </label>
        </div>
      </div>

      <!-- Footer Summary & Apply -->
      <div class="mt-2.5 flex flex-wrap items-center justify-between gap-2">
        <div class="flex flex-wrap items-center gap-2 text-xs text-gray-600 dark:text-gray-400">
          <span>
            {{
              t('admin.accounts.mappingBatch.summary', {
                selected: preview.selectedCount,
                changed: preview.changedSelectedCount
              })
            }}
          </span>
          <span
            v-if="preview.blockingErrorCount > 0"
            class="font-medium text-red-600 dark:text-red-400"
          >
            {{ t('admin.accounts.mappingBatch.blockingErrors', { count: preview.blockingErrorCount }) }}
          </span>
        </div>

        <div class="flex items-center gap-2">
          <button
            type="button"
            @click="isOpen = false"
            class="rounded-lg border border-gray-200 bg-white px-3 py-1.5 text-xs font-medium text-gray-600 hover:bg-gray-100 dark:border-dark-500 dark:bg-dark-700 dark:text-gray-300 dark:hover:bg-dark-600"
          >
            {{ t('common.cancel') }}
          </button>
          <button
            type="button"
            data-testid="mapping-batch-apply"
            :disabled="!preview.canApply"
            @click="applyBatch"
            class="rounded-lg bg-purple-600 px-3 py-1.5 text-xs font-medium text-white transition-colors hover:bg-purple-700 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {{ t('admin.accounts.mappingBatch.applyAction', { count: preview.selectedCount }) }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { accountsAPI, type SyncUpstreamPreviewParams } from '@/api/admin/accounts'
import Icon from '@/components/icons/Icon.vue'
import type { ModelMappingEntry } from '@/composables/useModelWhitelist'
import { supportsUpstreamModelSync } from '@/constants/platformCatalog'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import {
  applyModelMappingBatch,
  buildModelMappingPreview,
  buildSourceBatchRows,
  filterModelMappingBatchRows,
  MODEL_MAPPING_REGEX_PRESETS,
  type ModelMappingBatchSource,
  type ModelMappingConflictPolicy,
  type ModelMappingPreviewError,
  type ModelMappingRegexField,
  type ModelMappingRegexPreset
} from '@/utils/modelMappingRegex'

const { t } = useI18n()
const appStore = useAppStore()

const props = withDefaults(
  defineProps<{
    modelMappings: ModelMappingEntry[]
    allowedModels?: string[]
    allowWhitelistSource?: boolean
    platform?: string
    platforms?: string[]
    accountId?: number
    syncCredentials?: {
      platform: string
      type: string
      base_url?: string
      api_key: string
    }
    showUpstreamSync?: boolean
  }>(),
  {
    allowedModels: () => [],
    allowWhitelistSource: true,
    showUpstreamSync: true
  }
)

const emit = defineEmits<{
  'update:modelMappings': [value: ModelMappingEntry[]]
  'update:allowedModels': [value: string[]]
  'upstream-synced': []
  'upstream-sync-finished': []
}>()

const presets = MODEL_MAPPING_REGEX_PRESETS

const isOpen = ref(false)
const activeSource = ref<ModelMappingBatchSource>('existing')
const upstreamModels = ref<string[]>([])
const isSyncingUpstream = ref(false)
let syncVersion = 0

const pattern = ref('')
const replacement = ref('')
const flagGlobal = ref(false)
const flagIgnoreCase = ref(false)
const targetField = ref<ModelMappingRegexField>('from')
const filterQuery = ref('')
const filterUseRegex = ref(false)
const conflictPolicy = ref<ModelMappingConflictPolicy>('skip')
const selectedIds = ref<Set<string>>(new Set())

const undoSnapshot = ref<{
  modelMappings: ModelMappingEntry[]
  allowedModels: string[]
} | null>(null)

const normalizedPlatforms = computed(() => {
  const raw =
    props.platforms && props.platforms.length > 0
      ? props.platforms
      : props.platform
        ? [props.platform]
        : []
  return Array.from(
    new Set(raw.map((p) => p?.trim()).filter((p): p is string => Boolean(p)))
  )
})

const canSyncUpstream = computed(() => {
  if (props.accountId) {
    if (normalizedPlatforms.value.length === 0) return true
    return normalizedPlatforms.value.some(supportsUpstreamModelSync)
  }
  if (props.syncCredentials) {
    return supportsUpstreamModelSync(props.syncCredentials.platform)
  }
  return false
})

watch(
  () => props.accountId,
  () => {
    syncVersion += 1
    isSyncingUpstream.value = false
    upstreamModels.value = []
    undoSnapshot.value = null
    isOpen.value = false
    selectedIds.value = new Set()
  }
)

onUnmounted(() => {
  syncVersion += 1
})

const allSourceRows = computed(() =>
  buildSourceBatchRows(activeSource.value, {
    upstreamModels: upstreamModels.value,
    allowedModels: props.allowedModels,
    modelMappings: props.modelMappings
  })
)

const filteredResult = computed(() =>
  filterModelMappingBatchRows(allSourceRows.value, filterQuery.value, filterUseRegex.value)
)

const visibleRows = computed(() => filteredResult.value.rows)
const filterError = computed(() => filteredResult.value.error)

const preview = computed(() =>
  buildModelMappingPreview({
    source: activeSource.value,
    rows: visibleRows.value,
    selectedIds: selectedIds.value,
    existingMappings: props.modelMappings,
    regexOptions: {
      pattern: pattern.value,
      replacement: replacement.value,
      global: flagGlobal.value,
      ignoreCase: flagIgnoreCase.value,
      targetField: targetField.value
    }
  })
)

const selectAllSourceRows = () => {
  selectedIds.value = new Set(allSourceRows.value.map((r) => r.id))
}

const selectVisibleRows = () => {
  selectedIds.value = new Set(visibleRows.value.map((r) => r.id))
}

const selectMatchedRows = () => {
  selectedIds.value = new Set(preview.value.rows.filter((r) => r.matched).map((r) => r.id))
}

const clearSelection = () => {
  selectedIds.value = new Set()
}

const toggleRow = (id: string) => {
  const next = new Set(selectedIds.value)
  if (next.has(id)) {
    next.delete(id)
  } else {
    next.add(id)
  }
  selectedIds.value = next
}

const applyPreset = (preset: ModelMappingRegexPreset) => {
  pattern.value = preset.pattern
  replacement.value = preset.replacement
  flagGlobal.value = preset.global
  flagIgnoreCase.value = preset.ignoreCase
  targetField.value = preset.targetField
}

const fetchUpstreamModels = async (forceRefresh = false) => {
  if (isSyncingUpstream.value || !canSyncUpstream.value) return
  if (!forceRefresh && upstreamModels.value.length > 0) {
    selectAllSourceRows()
    return
  }

  const version = ++syncVersion
  const isCurrent = () => version === syncVersion
  isSyncingUpstream.value = true
  try {
    let result
    if (props.accountId) {
      result = await accountsAPI.syncUpstreamModels(props.accountId)
    } else if (props.syncCredentials) {
      result = await accountsAPI.syncUpstreamModelsPreview(
        props.syncCredentials as SyncUpstreamPreviewParams
      )
    } else {
      return
    }
    if (!isCurrent()) return

    if (result.discovery_source === 'configured_fallback') {
      appStore.showWarning(t('admin.accounts.grokModelDiagnostics.configuredFallback'))
      return
    }

    const fetched = result.models.map((m) => m.trim()).filter(Boolean)
    if (fetched.length === 0) {
      appStore.showInfo(t('admin.accounts.syncUpstreamModelsEmpty'))
      upstreamModels.value = []
      selectedIds.value = new Set()
      return
    }

    if (!props.accountId) {
      emit('upstream-synced')
    }
    upstreamModels.value = fetched
    selectAllSourceRows()
  } catch (error) {
    if (!isCurrent()) return
    const message = extractApiErrorMessage(error, t('admin.accounts.syncUpstreamModelsFailed'))
    appStore.showError(t('admin.accounts.syncUpstreamModelsError', { message }))
  } finally {
    if (isCurrent()) {
      isSyncingUpstream.value = false
      if (props.accountId) {
        emit('upstream-sync-finished')
      }
    }
  }
}

const switchSource = (source: ModelMappingBatchSource) => {
  activeSource.value = source
  filterQuery.value = ''
  if (source === 'upstream') {
    targetField.value = 'from'
    if (upstreamModels.value.length === 0) {
      void fetchUpstreamModels(false)
    } else {
      selectAllSourceRows()
    }
    return
  }
  if (source === 'whitelist') {
    targetField.value = 'from'
  }
  selectAllSourceRows()
}

const openPanel = (source: ModelMappingBatchSource) => {
  if (isOpen.value && activeSource.value === source) {
    isOpen.value = false
    return
  }
  isOpen.value = true
  switchSource(source)
}

const applyBatch = () => {
  if (!preview.value.canApply) return

  undoSnapshot.value = {
    modelMappings: props.modelMappings.map((m) => ({ from: m.from, to: m.to })),
    allowedModels: [...props.allowedModels]
  }

  const result = applyModelMappingBatch({
    source: activeSource.value,
    preview: preview.value,
    conflictPolicy: conflictPolicy.value,
    existingMappings: props.modelMappings,
    allowedModels: props.allowedModels
  })

  emit('update:modelMappings', result.modelMappings)
  if (result.removedWhitelistCount > 0) {
    emit('update:allowedModels', result.allowedModels)
  }

  appStore.showSuccess(
    t('admin.accounts.mappingBatch.applySuccess', {
      added: result.addedCount,
      updated: result.updatedCount,
      skipped: result.skippedCount
    })
  )

  if (activeSource.value === 'whitelist' && result.allowedModels.length === 0) {
    isOpen.value = false
  } else {
    // Refresh selection against updated source rows so consecutive regex passes work smoothly
    if (activeSource.value === 'existing') {
      selectedIds.value = new Set(result.modelMappings.map((_, idx) => `existing:${idx}`))
    }
  }
}

const undoLastApply = () => {
  if (!undoSnapshot.value) return
  emit('update:modelMappings', undoSnapshot.value.modelMappings)
  if (props.allowWhitelistSource) {
    emit('update:allowedModels', undoSnapshot.value.allowedModels)
  }
  undoSnapshot.value = null
  appStore.showInfo(t('admin.accounts.mappingBatch.undoSuccess'))
}

const errorLabel = (err: ModelMappingPreviewError): string => {
  switch (err) {
    case 'empty_from':
      return t('admin.accounts.mappingBatch.errors.emptyFrom')
    case 'empty_to':
      return t('admin.accounts.mappingBatch.errors.emptyTo')
    case 'invalid_wildcard_from':
      return t('admin.accounts.wildcardOnlyAtEnd')
    case 'wildcard_target':
      return t('admin.accounts.targetNoWildcard')
    case 'duplicate_in_batch':
      return t('admin.accounts.mappingBatch.errors.duplicateInBatch')
  }
}

const sourceTabClass = (source: ModelMappingBatchSource) => [
  'rounded-md px-2.5 py-1 text-xs font-medium transition-colors',
  activeSource.value === source
    ? 'bg-purple-100 text-purple-700 dark:bg-purple-900/40 dark:text-purple-300'
    : 'text-gray-600 hover:bg-gray-200/70 dark:text-gray-400 dark:hover:bg-dark-700'
]

const flagButtonClass = (active: boolean) => [
  'h-[34px] min-w-[30px] rounded-lg border px-2 font-mono text-xs font-semibold transition-colors',
  active
    ? 'border-purple-500 bg-purple-100 text-purple-700 dark:border-purple-500 dark:bg-purple-900/40 dark:text-purple-300'
    : 'border-gray-300 bg-white text-gray-500 hover:bg-gray-100 dark:border-dark-500 dark:bg-dark-700 dark:text-gray-400 dark:hover:bg-dark-600'
]

const pillClass = (active: boolean) => [
  'rounded-md px-2 py-0.5 text-[11px] font-medium transition-colors',
  active
    ? 'bg-purple-600 text-white'
    : 'border border-gray-200 bg-white text-gray-600 hover:bg-gray-100 dark:border-dark-500 dark:bg-dark-700 dark:text-gray-300 dark:hover:bg-dark-600'
]
</script>
