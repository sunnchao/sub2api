import { describe, expect, it, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import ModelMappingBatchPanel from '../ModelMappingBatchPanel.vue'

const { syncUpstreamModels, syncUpstreamModelsPreview } = vi.hoisted(() => ({
  syncUpstreamModels: vi.fn(),
  syncUpstreamModelsPreview: vi.fn()
}))

vi.mock('@/api/admin/accounts', () => ({
  accountsAPI: {
    syncUpstreamModels,
    syncUpstreamModelsPreview
  }
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) =>
        params ? `${key}:${JSON.stringify(params)}` : key
    })
  }
})

describe('ModelMappingBatchPanel', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    syncUpstreamModels.mockReset()
    syncUpstreamModelsPreview.mockReset()
  })

  it('fetches upstream models, filters + applies regex replace, and supports single-step undo', async () => {
    syncUpstreamModels.mockResolvedValue({
      models: ['[AIS]gemini-3.8-flash', '[AIS]gemini-4-argon', '[E]gemini-3.8-flash'],
      discovery_source: 'upstream'
    })

    const wrapper = mount(ModelMappingBatchPanel, {
      props: {
        modelMappings: [],
        allowedModels: [],
        platform: 'gemini',
        accountId: 134
      },
      global: {
        stubs: { Icon: true }
      }
    })

    await wrapper.get('[data-testid="mapping-batch-open-upstream"]').trigger('click')
    await flushPromises()

    expect(syncUpstreamModels).toHaveBeenCalledWith(134)
    expect(wrapper.findAll('[data-testid="mapping-batch-preview-row"]')).toHaveLength(3)

    // Filter to [AIS] only and select filtered rows
    await wrapper.get('[data-testid="mapping-batch-filter"]').setValue('[AIS]')
    expect(wrapper.findAll('[data-testid="mapping-batch-preview-row"]')).toHaveLength(2)
    await wrapper.get('[data-testid="mapping-batch-select-visible"]').trigger('click')

    // Apply built-in strip bracket prefix preset
    await wrapper.get('[data-testid="mapping-batch-preset-strip-bracket-prefix"]').trigger('click')
    expect((wrapper.get('[data-testid="mapping-batch-apply"]').element as HTMLButtonElement).disabled).toBe(false)

    await wrapper.get('[data-testid="mapping-batch-apply"]').trigger('click')

    const emitted = wrapper.emitted('update:modelMappings')
    expect(emitted).toBeTruthy()
    expect(emitted![0][0]).toEqual([
      { from: 'gemini-3.8-flash', to: '[AIS]gemini-3.8-flash' },
      { from: 'gemini-4-argon', to: '[AIS]gemini-4-argon' }
    ])

    // Undo restores original empty mappings
    await wrapper.get('[data-testid="mapping-batch-undo"]').trigger('click')
    expect(wrapper.emitted('update:modelMappings')![1][0]).toEqual([])
  })

  it('blocks apply on duplicate from collision and removes converted items from whitelist', async () => {
    const wrapper = mount(ModelMappingBatchPanel, {
      props: {
        modelMappings: [],
        allowedModels: ['[AIS]gemini-3.8-flash', '[E]gemini-3.8-flash', 'keep-me'],
        platform: 'gemini'
      },
      global: {
        stubs: { Icon: true }
      }
    })

    await wrapper.get('[data-testid="mapping-batch-open-whitelist"]').trigger('click')
    await wrapper.get('[data-testid="mapping-batch-preset-strip-bracket-prefix"]').trigger('click')

    // Both [AIS]gemini-3.8-flash and [E]gemini-3.8-flash collapse to gemini-3.8-flash -> blocked
    expect(wrapper.findAll('[data-testid="mapping-batch-row-error"]')).toHaveLength(2)
    expect((wrapper.get('[data-testid="mapping-batch-apply"]').element as HTMLButtonElement).disabled).toBe(true)

    // Filter to [AIS] only and select filtered -> collision resolved
    await wrapper.get('[data-testid="mapping-batch-filter"]').setValue('[AIS]')
    await wrapper.get('[data-testid="mapping-batch-select-visible"]').trigger('click')
    expect((wrapper.get('[data-testid="mapping-batch-apply"]').element as HTMLButtonElement).disabled).toBe(false)

    await wrapper.get('[data-testid="mapping-batch-apply"]').trigger('click')
    expect(wrapper.emitted('update:modelMappings')![0][0]).toEqual([
      { from: 'gemini-3.8-flash', to: '[AIS]gemini-3.8-flash' }
    ])
    expect(wrapper.emitted('update:allowedModels')![0][0]).toEqual([
      '[E]gemini-3.8-flash',
      'keep-me'
    ])
  })

  it('batch-updates existing mappings target column via regex and supports overwrite on conflict', async () => {
    const wrapper = mount(ModelMappingBatchPanel, {
      props: {
        modelMappings: [
          { from: 'gemini-3.8-flash', to: 'gemini-3.8-flash' },
          { from: 'gemini-4-argon', to: 'gemini-4-argon' }
        ],
        allowedModels: [],
        platform: 'gemini'
      },
      global: {
        stubs: { Icon: true }
      }
    })

    await wrapper.get('[data-testid="mapping-batch-open-existing"]').trigger('click')
    await wrapper.get('[data-testid="mapping-batch-target-to"]').trigger('click')
    await wrapper.get('[data-testid="mapping-batch-pattern"]').setValue('^(gemini-.+)$')
    await wrapper.get('[data-testid="mapping-batch-replacement"]').setValue('[AIS]$1')
    await wrapper.get('[data-testid="mapping-batch-apply"]').trigger('click')

    expect(wrapper.emitted('update:modelMappings')![0][0]).toEqual([
      { from: 'gemini-3.8-flash', to: '[AIS]gemini-3.8-flash' },
      { from: 'gemini-4-argon', to: '[AIS]gemini-4-argon' }
    ])
  })
})
