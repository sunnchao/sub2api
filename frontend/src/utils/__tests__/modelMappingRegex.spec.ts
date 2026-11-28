import { describe, expect, it } from 'vitest'
import {
  applyModelMappingBatch,
  buildModelMappingPreview,
  buildSourceBatchRows,
  compileModelMappingRegex,
  filterModelMappingBatchRows,
  MODEL_MAPPING_REGEX_PRESETS
} from '../modelMappingRegex'

describe('modelMappingRegex', () => {
  it('compiles valid regex with flags and reports invalid regex errors', () => {
    const empty = compileModelMappingRegex('', { global: false, ignoreCase: false })
    expect(empty.regex).toBeNull()
    expect(empty.error).toBeNull()

    const valid = compileModelMappingRegex('^\\[AIS\\]', { global: true, ignoreCase: true })
    expect(valid.error).toBeNull()
    expect(valid.regex?.flags).toContain('g')
    expect(valid.regex?.flags).toContain('i')

    const invalid = compileModelMappingRegex('[unclosed', { global: false, ignoreCase: false })
    expect(invalid.regex).toBeNull()
    expect(invalid.error).toBeTruthy()
  })

  it('filters rows by keyword or regex', () => {
    const rows = buildSourceBatchRows('upstream', {
      upstreamModels: [
        '[AIS]gemini-3.8-flash',
        '[E]gemini-3.8-flash',
        '[AIS]gemini-4-argon',
        'gpt-5'
      ],
      allowedModels: [],
      modelMappings: []
    })

    const kw = filterModelMappingBatchRows(rows, 'ais', false)
    expect(kw.error).toBeNull()
    expect(kw.rows.map((r) => r.originalTo)).toEqual(['[AIS]gemini-3.8-flash', '[AIS]gemini-4-argon'])

    const rx = filterModelMappingBatchRows(rows, '^\\[AIS\\]gemini-', true)
    expect(rx.error).toBeNull()
    expect(rx.rows.map((r) => r.originalTo)).toEqual(['[AIS]gemini-3.8-flash', '[AIS]gemini-4-argon'])

    const badRx = filterModelMappingBatchRows(rows, '[bad', true)
    expect(badRx.error).toBeTruthy()
    expect(badRx.rows).toEqual([])
  })

  it('strips relay bracket prefixes/suffixes with presets and builds preview', () => {
    const rows = buildSourceBatchRows('upstream', {
      upstreamModels: [
        '[AIS]gemini-3.8-flash',
        '[gcli转] gemini-3.1-pro-preview [假流]'
      ],
      allowedModels: [],
      modelMappings: []
    })
    const stripBoth = MODEL_MAPPING_REGEX_PRESETS.find((p) => p.id === 'strip-bracket-both')!

    const preview = buildModelMappingPreview({
      source: 'upstream',
      rows,
      selectedIds: new Set(rows.map((r) => r.id)),
      existingMappings: [],
      regexOptions: {
        pattern: stripBoth.pattern,
        replacement: stripBoth.replacement,
        global: stripBoth.global,
        ignoreCase: stripBoth.ignoreCase,
        targetField: stripBoth.targetField
      }
    })

    expect(preview.canApply).toBe(true)
    expect(preview.blockingErrorCount).toBe(0)
    expect(preview.changedSelectedCount).toBe(2)
    expect(preview.rows[0].nextFrom).toBe('gemini-3.8-flash')
    expect(preview.rows[0].nextTo).toBe('[AIS]gemini-3.8-flash')
    expect(preview.rows[1].nextFrom).toBe('gemini-3.1-pro-preview')
    expect(preview.rows[1].nextTo).toBe('[gcli转] gemini-3.1-pro-preview [假流]')
  })

  it('blocks apply on duplicate from in batch or empty replacement', () => {
    const rows = buildSourceBatchRows('upstream', {
      upstreamModels: ['[AIS]gemini-3.8-flash', '[E]gemini-3.8-flash'],
      allowedModels: [],
      modelMappings: []
    })

    const dupPreview = buildModelMappingPreview({
      source: 'upstream',
      rows,
      selectedIds: new Set(rows.map((r) => r.id)),
      existingMappings: [],
      regexOptions: {
        pattern: '^\\[[^\\]]+\\]',
        replacement: '',
        global: false,
        ignoreCase: false,
        targetField: 'from'
      }
    })
    expect(dupPreview.canApply).toBe(false)
    expect(dupPreview.blockingErrorCount).toBe(2)
    expect(dupPreview.rows.every((r) => r.error === 'duplicate_in_batch')).toBe(true)

    const emptyPreview = buildModelMappingPreview({
      source: 'upstream',
      rows: [rows[0]],
      selectedIds: new Set([rows[0].id]),
      existingMappings: [],
      regexOptions: {
        pattern: '.*',
        replacement: '',
        global: false,
        ignoreCase: false,
        targetField: 'from'
      }
    })
    expect(emptyPreview.canApply).toBe(false)
    expect(emptyPreview.rows[0].error).toBe('empty_from')
  })

  it('respects skip vs overwrite conflict policy on upstream import', () => {
    const existing = [{ from: 'gemini-3.8-flash', to: '[OLD]gemini-3.8-flash' }]
    const rows = buildSourceBatchRows('upstream', {
      upstreamModels: ['[AIS]gemini-3.8-flash', '[AIS]gemini-4-argon'],
      allowedModels: [],
      modelMappings: existing
    })
    const preview = buildModelMappingPreview({
      source: 'upstream',
      rows,
      selectedIds: new Set(rows.map((r) => r.id)),
      existingMappings: existing,
      regexOptions: {
        pattern: '^\\[AIS\\]',
        replacement: '',
        global: false,
        ignoreCase: false,
        targetField: 'from'
      }
    })
    expect(preview.canApply).toBe(true)
    expect(preview.conflictSelectedCount).toBe(1)

    const skipped = applyModelMappingBatch({
      source: 'upstream',
      preview,
      conflictPolicy: 'skip',
      existingMappings: existing,
      allowedModels: []
    })
    expect(skipped.addedCount).toBe(1)
    expect(skipped.skippedCount).toBe(1)
    expect(skipped.updatedCount).toBe(0)
    expect(skipped.modelMappings).toEqual([
      { from: 'gemini-3.8-flash', to: '[OLD]gemini-3.8-flash' },
      { from: 'gemini-4-argon', to: '[AIS]gemini-4-argon' }
    ])

    const overwritten = applyModelMappingBatch({
      source: 'upstream',
      preview,
      conflictPolicy: 'overwrite',
      existingMappings: existing,
      allowedModels: []
    })
    expect(overwritten.addedCount).toBe(1)
    expect(overwritten.updatedCount).toBe(1)
    expect(overwritten.skippedCount).toBe(0)
    expect(overwritten.modelMappings).toEqual([
      { from: 'gemini-3.8-flash', to: '[AIS]gemini-3.8-flash' },
      { from: 'gemini-4-argon', to: '[AIS]gemini-4-argon' }
    ])
  })

  it('removes converted models from allowedModels when converting from whitelist', () => {
    const allowed = ['[AIS]gemini-3.8-flash', '[AIS]gemini-4-argon', 'keep-in-whitelist']
    const rows = buildSourceBatchRows('whitelist', {
      upstreamModels: [],
      allowedModels: allowed,
      modelMappings: []
    })
    const selectedIds = new Set([rows[0].id, rows[1].id])
    const preview = buildModelMappingPreview({
      source: 'whitelist',
      rows,
      selectedIds,
      existingMappings: [],
      regexOptions: {
        pattern: '^\\[AIS\\]',
        replacement: '',
        global: false,
        ignoreCase: false,
        targetField: 'from'
      }
    })

    const res = applyModelMappingBatch({
      source: 'whitelist',
      preview,
      conflictPolicy: 'skip',
      existingMappings: [],
      allowedModels: allowed
    })
    expect(res.addedCount).toBe(2)
    expect(res.removedWhitelistCount).toBe(2)
    expect(res.allowedModels).toEqual(['keep-in-whitelist'])
    expect(res.modelMappings).toEqual([
      { from: 'gemini-3.8-flash', to: '[AIS]gemini-3.8-flash' },
      { from: 'gemini-4-argon', to: '[AIS]gemini-4-argon' }
    ])
  })

  it('supports capture groups and updating target column on existing mappings while guarding collisions', () => {
    const existing = [
      { from: 'gemini-3.8-flash', to: 'gemini-3.8-flash' },
      { from: 'gemini-4-argon', to: 'gemini-4-argon' }
    ]
    const rows = buildSourceBatchRows('existing', {
      upstreamModels: [],
      allowedModels: [],
      modelMappings: existing
    })

    // Add [AIS] prefix to `to` column using capture group
    const preview = buildModelMappingPreview({
      source: 'existing',
      rows,
      selectedIds: new Set(rows.map((r) => r.id)),
      existingMappings: existing,
      regexOptions: {
        pattern: '^(gemini-.+)$',
        replacement: '[AIS]$1',
        global: false,
        ignoreCase: false,
        targetField: 'to'
      }
    })
    expect(preview.canApply).toBe(true)
    const applied = applyModelMappingBatch({
      source: 'existing',
      preview,
      conflictPolicy: 'skip',
      existingMappings: existing,
      allowedModels: []
    })
    expect(applied.updatedCount).toBe(2)
    expect(applied.modelMappings).toEqual([
      { from: 'gemini-3.8-flash', to: '[AIS]gemini-3.8-flash' },
      { from: 'gemini-4-argon', to: '[AIS]gemini-4-argon' }
    ])

    // Collision with an unselected existing row blocks apply
    const collisionPreview = buildModelMappingPreview({
      source: 'existing',
      rows,
      selectedIds: new Set([rows[0].id]),
      existingMappings: existing,
      regexOptions: {
        pattern: 'gemini-3\\.8-flash',
        replacement: 'gemini-4-argon',
        global: false,
        ignoreCase: false,
        targetField: 'from'
      }
    })
    expect(collisionPreview.canApply).toBe(false)
    expect(collisionPreview.rows[0].error).toBe('duplicate_in_batch')
  })
})
