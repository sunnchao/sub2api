import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import enLocale from '@/i18n/locales/en'
import KeyUsageView from '../KeyUsageView.vue'

describe('KeyUsageView expiring balance breakdown', () => {
  const originalFetch = globalThis.fetch
  let wrapper: VueWrapper | null = null

  beforeEach(() => {
    setActivePinia(createPinia())
    vi.useFakeTimers()
    vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) =>
      setTimeout(() => cb(Date.now()), 0)
    )
    vi.stubGlobal('cancelAnimationFrame', (id: number) => clearTimeout(id))
  })

  afterEach(() => {
    if (wrapper) {
      wrapper.unmount()
      wrapper = null
    }
    vi.clearAllTimers()
    vi.useRealTimers()
    vi.unstubAllGlobals()
    globalThis.fetch = originalFetch
    vi.restoreAllMocks()
  })

  async function mountAndQuery(payload: Record<string, unknown>) {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => payload
    }) as never

    const i18n = createI18n({
      legacy: false,
      locale: 'en',
      messages: { en: enLocale }
    })

    wrapper = mount(KeyUsageView, {
      global: {
        plugins: [i18n],
        stubs: {
          LocaleSwitcher: true,
          RouterLink: { template: '<a><slot /></a>' }
        }
      }
    })

    const input = wrapper.find('input')
    await input.setValue('sk-test-key-123')
    const queryBtn = wrapper.findAll('button').find((b) => b.text().includes('Query'))
    expect(queryBtn).toBeTruthy()
    await queryBtn!.trigger('click')
    await flushPromises()
    return wrapper
  }

  it('displays total balance on ring and regular/expiring/nearest_expiring_at in detail rows', async () => {
    const view = await mountAndQuery({
      mode: 'unrestricted',
      isValid: true,
      planName: 'Wallet Balance',
      balance: 12.5,
      regular_balance: 10,
      expiring_balance: 2.5,
      nearest_expiring_at: '2026-11-27T15:59:00Z',
      remaining: 12.5,
      unit: 'USD',
      usage: {
        today: { requests: 0, input_tokens: 0, output_tokens: 0, total_tokens: 0, actual_cost: 0 },
        total: { requests: 0, input_tokens: 0, output_tokens: 0, total_tokens: 0, actual_cost: 0 }
      },
      model_stats: [],
      daily_usage: []
    })

    expect(view.text()).toContain('$12.50')
    expect(view.text()).toContain('Regular Balance')
    expect(view.text()).toContain('$10.00')
    expect(view.text()).toContain('Expiring Balance')
    expect(view.text()).toContain('$2.50')
    expect(view.text()).toContain('Nearest Expiration')
  })

  it('renders negative regular_balance as -$X.XX while keeping null/NaN and negative non-balance fields as -', async () => {
    const view = await mountAndQuery({
      mode: 'unrestricted',
      isValid: true,
      planName: 'Wallet Balance',
      balance: 3.0,
      regular_balance: -2.0,
      expiring_balance: 5.0,
      remaining: 3.0,
      unit: 'USD',
      usage: {
        today: { requests: 1, input_tokens: 1, output_tokens: 1, total_tokens: 2, actual_cost: -1 },
        total: { requests: 1, input_tokens: 1, output_tokens: 1, total_tokens: 2, actual_cost: Number.NaN }
      },
      model_stats: [],
      daily_usage: []
    })

    expect(view.text()).toContain('Regular Balance')
    expect(view.text()).toContain('-$2.00')
    expect(view.text()).toContain('$5.00')

    // Non-balance fields with negative or NaN values still render '-'
    const statCells = view.findAll('.tabular-nums')
    expect(statCells.some((cell) => cell.text() === '-')).toBe(true)
  })
})
