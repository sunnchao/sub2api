import { isValidWildcardPattern, type ModelMappingEntry } from '@/composables/useModelWhitelist'

export type ModelMappingBatchSource = 'upstream' | 'whitelist' | 'existing'
export type ModelMappingRegexField = 'from' | 'to'
export type ModelMappingConflictPolicy = 'skip' | 'overwrite'

export type ModelMappingPreviewError =
  | 'empty_from'
  | 'empty_to'
  | 'invalid_wildcard_from'
  | 'wildcard_target'
  | 'duplicate_in_batch'

export interface ModelMappingBatchRow {
  id: string
  originalFrom: string
  originalTo: string
  sourceIndex?: number
  sourceWhitelistModel?: string
}

export interface ModelMappingRegexOptions {
  pattern: string
  replacement: string
  global: boolean
  ignoreCase: boolean
  targetField: ModelMappingRegexField
}

export interface ModelMappingPreviewRow {
  id: string
  originalFrom: string
  originalTo: string
  nextFrom: string
  nextTo: string
  matched: boolean
  changed: boolean
  selected: boolean
  error: ModelMappingPreviewError | null
  conflictWithExisting: boolean
  existingTarget?: string
  sourceIndex?: number
  sourceWhitelistModel?: string
}

export interface ModelMappingPreviewSummary {
  rows: ModelMappingPreviewRow[]
  regexError: string | null
  selectedCount: number
  changedSelectedCount: number
  conflictSelectedCount: number
  blockingErrorCount: number
  canApply: boolean
}

export interface ModelMappingRegexPreset {
  id: string
  labelKey: string
  pattern: string
  replacement: string
  global: boolean
  ignoreCase: boolean
  targetField: ModelMappingRegexField
}

export const MODEL_MAPPING_REGEX_PRESETS: ModelMappingRegexPreset[] = [
  {
    id: 'strip-bracket-prefix',
    labelKey: 'admin.accounts.mappingBatch.presets.stripBracketPrefix',
    pattern: '^\\[[^\\]]+\\]\\s*',
    replacement: '',
    global: false,
    ignoreCase: false,
    targetField: 'from'
  },
  {
    id: 'strip-bracket-both',
    labelKey: 'admin.accounts.mappingBatch.presets.stripBracketBoth',
    pattern: '^\\[[^\\]]+\\]\\s*|\\s*\\[[^\\]]+\\]$',
    replacement: '',
    global: true,
    ignoreCase: false,
    targetField: 'from'
  },
  {
    id: 'strip-provider-prefix',
    labelKey: 'admin.accounts.mappingBatch.presets.stripProviderPrefix',
    pattern: '^[^/]+/',
    replacement: '',
    global: false,
    ignoreCase: false,
    targetField: 'from'
  },
  {
    id: 'strip-colon-suffix',
    labelKey: 'admin.accounts.mappingBatch.presets.stripColonSuffix',
    pattern: ':[^:]+$',
    replacement: '',
    global: false,
    ignoreCase: false,
    targetField: 'from'
  },
  {
    id: 'add-prefix-to-target',
    labelKey: 'admin.accounts.mappingBatch.presets.addPrefixToTarget',
    pattern: '^',
    replacement: '[AIS]',
    global: false,
    ignoreCase: false,
    targetField: 'to'
  }
]

export function compileModelMappingRegex(
  pattern: string,
  options: { global: boolean; ignoreCase: boolean }
): { regex: RegExp | null; error: string | null } {
  if (!pattern) {
    return { regex: null, error: null }
  }
  let flags = ''
  if (options.global) flags += 'g'
  if (options.ignoreCase) flags += 'i'
  try {
    return { regex: new RegExp(pattern, flags), error: null }
  } catch (err) {
    return {
      regex: null,
      error: err instanceof Error ? err.message : String(err)
    }
  }
}

export function filterModelMappingBatchRows(
  rows: ModelMappingBatchRow[],
  query: string,
  useRegex: boolean
): { rows: ModelMappingBatchRow[]; error: string | null } {
  const trimmed = query.trim()
  if (!trimmed) {
    return { rows, error: null }
  }
  if (useRegex) {
    try {
      const regex = new RegExp(trimmed, 'i')
      return {
        rows: rows.filter((row) => regex.test(row.originalFrom) || regex.test(row.originalTo)),
        error: null
      }
    } catch (err) {
      return {
        rows: [],
        error: err instanceof Error ? err.message : String(err)
      }
    }
  }
  const needle = trimmed.toLowerCase()
  return {
    rows: rows.filter(
      (row) =>
        row.originalFrom.toLowerCase().includes(needle) ||
        row.originalTo.toLowerCase().includes(needle)
    ),
    error: null
  }
}

export function buildSourceBatchRows(
  source: ModelMappingBatchSource,
  params: {
    upstreamModels: string[]
    allowedModels: string[]
    modelMappings: ModelMappingEntry[]
  }
): ModelMappingBatchRow[] {
  if (source === 'upstream') {
    const seen = new Set<string>()
    const rows: ModelMappingBatchRow[] = []
    for (const raw of params.upstreamModels) {
      const model = raw.trim()
      if (!model || seen.has(model)) continue
      seen.add(model)
      rows.push({
        id: `upstream:${model}`,
        originalFrom: model,
        originalTo: model
      })
    }
    return rows
  }

  if (source === 'whitelist') {
    const seen = new Set<string>()
    const rows: ModelMappingBatchRow[] = []
    for (const raw of params.allowedModels) {
      const model = raw.trim()
      if (!model || seen.has(model)) continue
      seen.add(model)
      rows.push({
        id: `whitelist:${model}`,
        originalFrom: model,
        originalTo: model,
        sourceWhitelistModel: raw
      })
    }
    return rows
  }

  return params.modelMappings.map((entry, index) => ({
    id: `existing:${index}`,
    originalFrom: entry.from,
    originalTo: entry.to,
    sourceIndex: index
  }))
}

export function buildModelMappingPreview(params: {
  source: ModelMappingBatchSource
  rows: ModelMappingBatchRow[]
  selectedIds: ReadonlySet<string>
  existingMappings: ModelMappingEntry[]
  regexOptions: ModelMappingRegexOptions
}): ModelMappingPreviewSummary {
  const { source, rows, selectedIds, existingMappings, regexOptions } = params
  const { regex, error: regexError } = compileModelMappingRegex(regexOptions.pattern, {
    global: regexOptions.global,
    ignoreCase: regexOptions.ignoreCase
  })

  const existingByFrom = new Map<string, string>()
  for (const entry of existingMappings) {
    const from = entry.from.trim()
    if (from && !existingByFrom.has(from)) {
      existingByFrom.set(from, entry.to.trim())
    }
  }

  const previewRows: ModelMappingPreviewRow[] = rows.map((row) => {
    const selected = selectedIds.has(row.id)
    const origFrom = row.originalFrom
    const origTo = row.originalTo
    let nextFrom = origFrom
    let nextTo = origTo
    let matched = false

    if (regex) {
      const targetValue = regexOptions.targetField === 'from' ? origFrom : origTo
      regex.lastIndex = 0
      matched = regex.test(targetValue)
      if (matched) {
        regex.lastIndex = 0
        const replaced = targetValue.replace(regex, regexOptions.replacement).trim()
        if (regexOptions.targetField === 'from') {
          nextFrom = replaced
        } else {
          nextTo = replaced
        }
      }
    }

    const trimmedFrom = nextFrom.trim()
    const trimmedTo = nextTo.trim()
    const changed = trimmedFrom !== origFrom.trim() || trimmedTo !== origTo.trim()

    let rowError: ModelMappingPreviewError | null = null
    if (!trimmedFrom) {
      rowError = 'empty_from'
    } else if (!trimmedTo) {
      rowError = 'empty_to'
    } else if (!isValidWildcardPattern(trimmedFrom)) {
      rowError = 'invalid_wildcard_from'
    } else if (trimmedTo.includes('*')) {
      rowError = 'wildcard_target'
    }

    const existingTarget =
      source !== 'existing' && trimmedFrom ? existingByFrom.get(trimmedFrom) : undefined

    return {
      id: row.id,
      originalFrom: origFrom,
      originalTo: origTo,
      nextFrom: trimmedFrom,
      nextTo: trimmedTo,
      matched,
      changed,
      selected,
      error: rowError,
      conflictWithExisting: existingTarget !== undefined,
      existingTarget,
      sourceIndex: row.sourceIndex,
      sourceWhitelistModel: row.sourceWhitelistModel
    }
  })

  // Detect duplicate `from` collisions among selected rows (and unselected existing rows in `existing` mode)
  const fromCounts = new Map<string, number>()
  if (source === 'existing') {
    for (let i = 0; i < existingMappings.length; i++) {
      const rowId = `existing:${i}`
      if (selectedIds.has(rowId)) continue
      const from = existingMappings[i].from.trim()
      if (from) {
        fromCounts.set(from, (fromCounts.get(from) ?? 0) + 1)
      }
    }
  }
  for (const row of previewRows) {
    if (!row.selected || !row.nextFrom) continue
    fromCounts.set(row.nextFrom, (fromCounts.get(row.nextFrom) ?? 0) + 1)
  }
  for (const row of previewRows) {
    if (row.selected && !row.error && row.nextFrom && (fromCounts.get(row.nextFrom) ?? 0) > 1) {
      row.error = 'duplicate_in_batch'
    }
  }

  let selectedCount = 0
  let changedSelectedCount = 0
  let conflictSelectedCount = 0
  let blockingErrorCount = 0

  for (const row of previewRows) {
    if (!row.selected) continue
    selectedCount += 1
    if (row.changed) changedSelectedCount += 1
    if (row.conflictWithExisting) conflictSelectedCount += 1
    if (row.error) blockingErrorCount += 1
  }

  const canApply =
    !regexError &&
    selectedCount > 0 &&
    blockingErrorCount === 0 &&
    (source !== 'existing' || changedSelectedCount > 0)

  return {
    rows: previewRows,
    regexError,
    selectedCount,
    changedSelectedCount,
    conflictSelectedCount,
    blockingErrorCount,
    canApply
  }
}

export interface ApplyModelMappingBatchResult {
  modelMappings: ModelMappingEntry[]
  allowedModels: string[]
  addedCount: number
  updatedCount: number
  skippedCount: number
  removedWhitelistCount: number
}

export function applyModelMappingBatch(params: {
  source: ModelMappingBatchSource
  preview: ModelMappingPreviewSummary
  conflictPolicy: ModelMappingConflictPolicy
  existingMappings: ModelMappingEntry[]
  allowedModels: string[]
}): ApplyModelMappingBatchResult {
  const { source, preview, conflictPolicy, existingMappings, allowedModels } = params
  const nextMappings: ModelMappingEntry[] = existingMappings.map((m) => ({
    from: m.from,
    to: m.to
  }))
  let nextAllowed = [...allowedModels]

  let addedCount = 0
  let updatedCount = 0
  let skippedCount = 0
  let removedWhitelistCount = 0

  if (!preview.canApply) {
    return {
      modelMappings: nextMappings,
      allowedModels: nextAllowed,
      addedCount,
      updatedCount,
      skippedCount,
      removedWhitelistCount
    }
  }

  if (source === 'existing') {
    for (const row of preview.rows) {
      if (!row.selected || row.sourceIndex === undefined) continue
      const target = nextMappings[row.sourceIndex]
      if (!target) continue
      if (row.changed) {
        target.from = row.nextFrom
        target.to = row.nextTo
        updatedCount += 1
      }
    }
    return {
      modelMappings: nextMappings,
      allowedModels: nextAllowed,
      addedCount,
      updatedCount,
      skippedCount,
      removedWhitelistCount
    }
  }

  const indexByFrom = new Map<string, number>()
  for (let i = 0; i < nextMappings.length; i++) {
    const key = nextMappings[i].from.trim()
    if (key && !indexByFrom.has(key)) {
      indexByFrom.set(key, i)
    }
  }

  const whitelistToRemove = new Set<string>()

  for (const row of preview.rows) {
    if (!row.selected || row.error) continue
    const existingIdx = indexByFrom.get(row.nextFrom)
    if (existingIdx !== undefined) {
      if (conflictPolicy === 'overwrite') {
        nextMappings[existingIdx] = { from: row.nextFrom, to: row.nextTo }
        updatedCount += 1
        if (source === 'whitelist' && row.sourceWhitelistModel !== undefined) {
          whitelistToRemove.add(row.sourceWhitelistModel)
        }
      } else {
        skippedCount += 1
      }
      continue
    }

    indexByFrom.set(row.nextFrom, nextMappings.length)
    nextMappings.push({ from: row.nextFrom, to: row.nextTo })
    addedCount += 1
    if (source === 'whitelist' && row.sourceWhitelistModel !== undefined) {
      whitelistToRemove.add(row.sourceWhitelistModel)
    }
  }

  if (whitelistToRemove.size > 0) {
    const before = nextAllowed.length
    nextAllowed = nextAllowed.filter((item) => !whitelistToRemove.has(item))
    removedWhitelistCount = before - nextAllowed.length
  }

  return {
    modelMappings: nextMappings,
    allowedModels: nextAllowed,
    addedCount,
    updatedCount,
    skippedCount,
    removedWhitelistCount
  }
}
