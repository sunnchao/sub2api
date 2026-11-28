import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import enLocale from '@/i18n/locales/en'
import CheckinView from '../CheckinView.vue'
import { checkinAPI } from '@/api/checkin'
import { useAppStore, useAuthStore } from '@/stores'

vi.mock('@/api/checkin', () => ({
  checkinAPI: {
    getStatus: vi.fn(),
    checkin: vi.fn(),
    getCalendar: vi.fn(),
    getRecords: vi.fn(),
    getLots: vi.fn()
  }
}))

const mockedAPI = vi.mocked(checkinAPI)

describe('views/user/CheckinView', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  function setupDefaultMocks(rulesDescription = '') {
    mockedAPI.getStatus.mockResolvedValue({
      enabled: true,
      checked_in_today: false,
      today_record: null,
      current_streak: 4,
      milestone_cycle_day: 4,
      next_milestone: { days: 7, bonus_amount: 1.0 },
      milestones: [
        { days: 3, bonus_amount: 0.3 },
        { days: 7, bonus_amount: 1.0 }
      ],
      milestone_cycle_repeat: true,
      reward_preview: {
        reward_mode: 'random',
        base_amount: 0,
        min_amount: 0.05,
        max_amount: 0.2,
        amount_precision: 2,
        validity_days: 2
      },
      eligibility: { eligible: true },
      captcha_required: false,
      expiring_summary: {
        total_active: 1.5,
        frozen_amount: 0.25,
        nearest_expires_at: '2026-11-28T00:00:00Z',
        nearest_amount: 0.5
      },
      rules_description: rulesDescription,
      server_date: '2026-11-27',
      timezone: 'Asia/Shanghai'
    })

    mockedAPI.getCalendar.mockResolvedValue({
      month: '2026-11',
      checked_dates: [
        { date: '2026-11-25', base_amount: 0.1, milestone_bonus_amount: 0.3, total_amount: 0.4, milestone_hit: true }
      ],
      month_total_amount: 0.4,
      month_checkin_days: 1
    })

    mockedAPI.getLots.mockResolvedValue({
      items: [
        {
          id: 10,
          user_id: 1,
          source_type: 'checkin',
          source_id: 1,
          initial_amount: 0.4,
          remaining_amount: 0.2,
          frozen_amount: 0,
          consumed_amount: 0.2,
          expired_amount: 0,
          expires_at: '2026-11-28T00:00:00Z',
          status: 'active',
          created_at: '2026-11-25T08:00:00Z',
          updated_at: '2026-11-25T08:00:00Z'
        }
      ],
      total: 1,
      page: 1,
      page_size: 10,
      pages: 1
    })

    mockedAPI.getRecords.mockResolvedValue({
      items: [
        {
          id: 1,
          user_id: 1,
          checkin_date: '2026-11-25',
          streak_days: 3,
          milestone_cycle_day: 3,
          base_amount: 0.1,
          milestone_bonus_amount: 0.3,
          total_amount: 0.4,
          validity_days: 2,
          expires_at: '2026-11-28T00:00:00Z',
          created_at: '2026-11-25T08:00:00Z'
        }
      ],
      total: 1,
      page: 1,
      page_size: 10,
      pages: 1
    })
  }

  function mountView() {
    const authStore = useAuthStore()
    vi.spyOn(authStore, 'refreshUser').mockResolvedValue(null as never)
    const i18n = createI18n({
      legacy: false,
      locale: 'en',
      messages: { en: enLocale }
    })
    return mount(CheckinView, {
      global: {
        plugins: [i18n],
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          Pagination: true,
          CaptchaChallenge: true
        }
      }
    })
  }

  it('renders hero summary, milestones, calendar navigation, lots/history tabs, and default rules', async () => {
    setupDefaultMocks('')
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.text()).toContain('$1.50')
    expect(wrapper.text()).toContain('$0.25')
    expect(wrapper.text()).toContain('3-Day Streak')
    expect(wrapper.text()).toContain('7-Day Streak')
    expect(wrapper.text()).toContain('2026-11')
    expect(wrapper.find('[data-testid="checkin-default-rules"]').exists()).toBe(true)

    // Navigate to previous month
    await wrapper.find('[data-testid="calendar-prev-month"]').trigger('click')
    await flushPromises()
    expect(mockedAPI.getCalendar).toHaveBeenCalledWith('2026-10')

    // Switch to records tab
    await wrapper.find('[data-testid="tab-history"]').trigger('click')
    await flushPromises()
    expect(mockedAPI.getRecords).toHaveBeenCalled()
    expect(wrapper.text()).toContain('2026-11-25')
  })

  it('sanitizes custom markdown rules and strips unsafe scripts', async () => {
    setupDefaultMocks('**Welcome** <script>alert("xss")</script>')
    const wrapper = mountView()
    await flushPromises()

    const customRules = wrapper.find('[data-testid="checkin-custom-rules"]')
    expect(customRules.exists()).toBe(true)
    expect(customRules.html()).toContain('<strong>Welcome</strong>')
    expect(customRules.html()).not.toContain('<script>')
  })

  it('reloads calendar for server_date month after checking in while viewing a previous month', async () => {
    setupDefaultMocks('')
    let statusCallCount = 0
    mockedAPI.getStatus.mockImplementation(async () => {
      statusCallCount += 1
      if (statusCallCount === 1) {
        return {
          enabled: true,
          checked_in_today: false,
          today_record: null,
          current_streak: 1,
          milestone_cycle_day: 1,
          next_milestone: { days: 3, bonus_amount: 0.3 },
          milestones: [{ days: 3, bonus_amount: 0.3 }],
          milestone_cycle_repeat: false,
          reward_preview: {
            reward_mode: 'fixed',
            base_amount: 0.5,
            min_amount: 0.5,
            max_amount: 0.5,
            amount_precision: 2,
            validity_days: 1
          },
          eligibility: { eligible: true },
          captcha_required: false,
          expiring_summary: { total_active: 0, frozen_amount: 0, nearest_amount: 0 },
          rules_description: '',
          server_date: '2026-11-27',
          timezone: 'Asia/Shanghai'
        }
      }
      return {
        enabled: true,
        checked_in_today: true,
        today_record: {
          id: 99,
          user_id: 1,
          checkin_date: '2026-11-27',
          streak_days: 2,
          milestone_cycle_day: 2,
          base_amount: 0.5,
          milestone_bonus_amount: 0,
          total_amount: 0.5,
          validity_days: 1,
          expires_at: '2026-11-28T00:00:00Z',
          created_at: '2026-11-27T08:00:00Z'
        },
        current_streak: 2,
        milestone_cycle_day: 2,
        next_milestone: { days: 3, bonus_amount: 0.3 },
        milestones: [{ days: 3, bonus_amount: 0.3 }],
        milestone_cycle_repeat: false,
        reward_preview: {
          reward_mode: 'fixed',
          base_amount: 0.5,
          min_amount: 0.5,
          max_amount: 0.5,
          amount_precision: 2,
          validity_days: 1
        },
        eligibility: { eligible: true },
        captcha_required: false,
        expiring_summary: { total_active: 0.5, frozen_amount: 0, nearest_amount: 0.5 },
        rules_description: '',
        server_date: '2026-11-27',
        timezone: 'Asia/Shanghai'
      }
    })
    mockedAPI.getCalendar.mockImplementation(async (month?: string) => {
      if (month === '2026-10') {
        return {
          month: '2026-10',
          checked_dates: [
            { date: '2026-10-15', base_amount: 0.1, milestone_bonus_amount: 0, total_amount: 0.1, milestone_hit: false }
          ],
          month_total_amount: 0.1,
          month_checkin_days: 1
        }
      }
      return {
        month: '2026-11',
        checked_dates: [
          { date: '2026-11-27', base_amount: 0.5, milestone_bonus_amount: 0, total_amount: 0.5, milestone_hit: false }
        ],
        month_total_amount: 0.5,
        month_checkin_days: 1
      }
    })
    mockedAPI.checkin.mockResolvedValue({
      record: {
        id: 99,
        user_id: 1,
        checkin_date: '2026-11-27',
        streak_days: 2,
        milestone_cycle_day: 2,
        base_amount: 0.5,
        milestone_bonus_amount: 0,
        total_amount: 0.5,
        validity_days: 1,
        expires_at: '2026-11-28T00:00:00Z',
        created_at: '2026-11-27T08:00:00Z'
      },
      expiring_summary: { total_active: 0.5, frozen_amount: 0, nearest_amount: 0.5 },
      total_available_balance: 0.5
    })

    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('[data-testid="calendar-prev-month"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('2026-10')
    expect(wrapper.text()).toContain('Total earned $0.10')

    mockedAPI.getCalendar.mockClear()
    await wrapper.find('[data-testid="checkin-submit-btn"]').trigger('click')
    await flushPromises()

    expect(mockedAPI.getCalendar).toHaveBeenCalledTimes(1)
    expect(mockedAPI.getCalendar).toHaveBeenCalledWith('2026-11')
    expect(wrapper.text()).toContain('2026-11')
    expect(wrapper.text()).toContain('Total earned $0.50')
  })

  it('formats 4-decimal rewards without truncation when amount_precision is 4', async () => {
    setupDefaultMocks('')
    const appStore = useAppStore()
    const successSpy = vi.spyOn(appStore, 'showSuccess')

    mockedAPI.getStatus.mockResolvedValue({
      enabled: true,
      checked_in_today: true,
      today_record: {
        id: 20,
        user_id: 1,
        checkin_date: '2026-11-27',
        streak_days: 1,
        milestone_cycle_day: 1,
        base_amount: 0.0025,
        milestone_bonus_amount: 0,
        total_amount: 0.0025,
        validity_days: 1,
        expires_at: '2026-11-28T00:00:00Z',
        created_at: '2026-11-27T08:00:00Z'
      },
      current_streak: 1,
      milestone_cycle_day: 1,
      next_milestone: null,
      milestones: [],
      milestone_cycle_repeat: false,
      reward_preview: {
        reward_mode: 'random',
        base_amount: 0,
        min_amount: 0.001,
        max_amount: 0.005,
        amount_precision: 4,
        validity_days: 1
      },
      eligibility: { eligible: true },
      captcha_required: false,
      expiring_summary: {
        total_active: 0.0025,
        frozen_amount: 0,
        nearest_expires_at: '2026-11-28T00:00:00Z',
        nearest_amount: 0.0025
      },
      rules_description: '',
      server_date: '2026-11-27',
      timezone: 'Asia/Shanghai'
    })
    mockedAPI.getCalendar.mockResolvedValue({
      month: '2026-11',
      checked_dates: [
        { date: '2026-11-27', base_amount: 0.0025, milestone_bonus_amount: 0, total_amount: 0.0025, milestone_hit: false }
      ],
      month_total_amount: 0.0025,
      month_checkin_days: 1
    })
    mockedAPI.getLots.mockResolvedValue({
      items: [
        {
          id: 30,
          user_id: 1,
          source_type: 'checkin',
          source_id: 20,
          initial_amount: 0.0025,
          remaining_amount: 0.0025,
          frozen_amount: 0,
          consumed_amount: 0,
          expired_amount: 0,
          expires_at: '2026-11-28T00:00:00Z',
          status: 'active',
          created_at: '2026-11-27T08:00:00Z',
          updated_at: '2026-11-27T08:00:00Z'
        }
      ],
      total: 1,
      page: 1,
      page_size: 10,
      pages: 1
    })

    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.text()).toContain('$0.0025')
    expect(wrapper.text()).toContain('+$0.0025')
    expect(successSpy).not.toHaveBeenCalled()
  })

  it('computes wrap-around next milestone days and step states cleanly when milestone_cycle_repeat is true', async () => {
    setupDefaultMocks('')
    mockedAPI.getStatus.mockResolvedValue({
      enabled: true,
      checked_in_today: true,
      today_record: {
        id: 10,
        user_id: 1,
        checkin_date: '2026-11-27',
        streak_days: 7,
        milestone_cycle_day: 7,
        base_amount: 0.1,
        milestone_bonus_amount: 1.0,
        total_amount: 1.1,
        validity_days: 1,
        expires_at: '2026-11-28T00:00:00Z',
        created_at: '2026-11-27T08:00:00Z'
      },
      current_streak: 7,
      milestone_cycle_day: 7,
      next_milestone: { days: 3, bonus_amount: 0.3 },
      milestones: [
        { days: 3, bonus_amount: 0.3 },
        { days: 7, bonus_amount: 1.0 }
      ],
      milestone_cycle_repeat: true,
      reward_preview: {
        reward_mode: 'fixed',
        base_amount: 0.1,
        min_amount: 0.1,
        max_amount: 0.1,
        amount_precision: 2,
        validity_days: 1
      },
      eligibility: { eligible: true },
      captcha_required: false,
      expiring_summary: { total_active: 1.1, frozen_amount: 0, nearest_amount: 1.1 },
      rules_description: '',
      server_date: '2026-11-27',
      timezone: 'Asia/Shanghai'
    })

    const wrapper = mountView()
    await flushPromises()

    const badge = wrapper.find('[data-testid="checkin-next-milestone-badge"]')
    expect(badge.text()).toContain('In 3d (+$0.30)')
    expect(badge.text()).not.toContain('In 0d')
    expect(wrapper.text()).toContain('Up Next')
    expect(wrapper.text()).not.toContain('Achieved')
  })
})
