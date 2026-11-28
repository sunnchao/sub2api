import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import enLocale from '@/i18n/locales/en'
import AdminCheckinView from '../CheckinView.vue'
import { adminAPI } from '@/api/admin'

vi.mock('@/api/admin', () => ({
  adminAPI: {
    checkin: {
      getStats: vi.fn(),
      listRecords: vi.fn()
    }
  }
}))

describe('views/admin/CheckinView', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('renders KPI stats, budget bar, and triggers filtered listRecords queries', async () => {
    vi.mocked(adminAPI.checkin.getStats).mockResolvedValue({
      today_checkins: 25,
      today_granted: 4.5,
      daily_budget_limit: 10,
      daily_budget_percent: 45,
      range_days: 30,
      range_total_granted: 100,
      range_total_consumed: 75,
      range_total_expired: 15,
      consumption_rate: 0.75,
      expiration_rate: 0.15,
      daily_trend: [
        { date: '2026-11-27', checkin_count: 25, granted_amount: 4.5, consumed_amount: 3.0, expired_amount: 0 }
      ]
    })
    vi.mocked(adminAPI.checkin.listRecords).mockResolvedValue({
      items: [
        {
          id: 1,
          user_id: 9,
          user_email: 'bob@example.com',
          checkin_date: '2026-11-27',
          streak_days: 5,
          milestone_cycle_day: 5,
          base_amount: 0.1,
          milestone_bonus_amount: 0,
          total_amount: 0.1,
          validity_days: 1,
          expires_at: '2026-11-28T00:00:00Z',
          client_ip: '1.2.3.4',
          created_at: '2026-11-27T08:00:00Z'
        }
      ],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1
    })

    const i18n = createI18n({
      legacy: false,
      locale: 'en',
      messages: { en: enLocale }
    })

    const wrapper = mount(AdminCheckinView, {
      global: {
        plugins: [i18n],
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          RouterLink: { template: '<a><slot /></a>' },
          Pagination: true
        }
      }
    })
    await flushPromises()

    expect(wrapper.text()).toContain('25')
    expect(wrapper.text()).toContain('75.0%')
    expect(wrapper.text()).toContain('15.0%')
    expect(wrapper.find('[data-testid="admin-checkin-budget-bar"]').attributes('style')).toContain('45%')
    expect(wrapper.text()).toContain('bob@example.com')

    await wrapper.find('[data-testid="filter-search"]').setValue('bob')
    await wrapper.find('[data-testid="filter-user-id"]').setValue('9')
    await wrapper.find('[data-testid="filter-client-ip"]').setValue('1.2.3.4')
    await wrapper.find('[data-testid="filter-start-date"]').setValue('2026-11-01')
    await wrapper.find('[data-testid="filter-apply-btn"]').trigger('click')
    await flushPromises()

    expect(adminAPI.checkin.listRecords).toHaveBeenLastCalledWith({
      page: 1,
      page_size: 20,
      user_id: 9,
      search: 'bob',
      client_ip: '1.2.3.4',
      start_date: '2026-11-01',
      end_date: undefined
    })

    await wrapper.find('[data-testid="filter-reset-btn"]').trigger('click')
    await flushPromises()
    expect(adminAPI.checkin.listRecords).toHaveBeenLastCalledWith({
      page: 1,
      page_size: 20,
      user_id: undefined,
      search: undefined,
      client_ip: undefined,
      start_date: undefined,
      end_date: undefined
    })

    // Fractional user_id is rejected / omitted
    await wrapper.find('[data-testid="filter-user-id"]').setValue('1.5')
    await wrapper.find('[data-testid="filter-apply-btn"]').trigger('click')
    await flushPromises()
    expect(adminAPI.checkin.listRecords).toHaveBeenLastCalledWith({
      page: 1,
      page_size: 20,
      user_id: undefined,
      search: undefined,
      client_ip: undefined,
      start_date: undefined,
      end_date: undefined
    })
  })
})
