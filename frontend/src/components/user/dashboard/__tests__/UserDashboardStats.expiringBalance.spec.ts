import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enLocale from '@/i18n/locales/en'
import UserDashboardStats from '../UserDashboardStats.vue'

describe('UserDashboardStats expiring balance display', () => {
  const baseStats = {
    total_api_keys: 2,
    active_api_keys: 2,
    total_requests: 10,
    total_input_tokens: 100,
    total_output_tokens: 50,
    total_cache_creation_tokens: 0,
    total_cache_read_tokens: 0,
    total_tokens: 150,
    total_cost: 0.1,
    total_actual_cost: 0.1,
    today_requests: 1,
    today_input_tokens: 10,
    today_output_tokens: 5,
    today_cache_creation_tokens: 0,
    today_cache_read_tokens: 0,
    today_tokens: 15,
    today_cost: 0.01,
    today_actual_cost: 0.01,
    average_duration_ms: 200,
    rpm: 1,
    tpm: 15
  }

  function mountStats(props: { balance: number; expiringBalance?: number; nearestExpiringAt?: string | null }) {
    const i18n = createI18n({
      legacy: false,
      locale: 'en',
      messages: { en: enLocale }
    })
    return mount(UserDashboardStats, {
      props: {
        stats: baseStats,
        isSimple: false,
        ...props
      },
      global: {
        plugins: [i18n]
      }
    })
  }

  it('shows total balance and expiring subline when expiringBalance > 0', () => {
    const wrapper = mountStats({
      balance: 15.25,
      expiringBalance: 5.25,
      nearestExpiringAt: '2026-11-27T23:59:59Z'
    })
    expect(wrapper.text()).toContain('$15.25')
    const subline = wrapper.find('[data-testid="stats-expiring-subline"]')
    expect(subline.exists()).toBe(true)
    expect(subline.text()).toContain('5.25')
  })

  it('hides expiring subline when expiringBalance is 0 or omitted', () => {
    const wrapper = mountStats({ balance: 10 })
    expect(wrapper.text()).toContain('$10.00')
    expect(wrapper.find('[data-testid="stats-expiring-subline"]').exists()).toBe(false)
  })
})
