import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import enLocale from '@/i18n/locales/en'
import UserDashboardCheckinCard from '../UserDashboardCheckinCard.vue'
import { checkinAPI } from '@/api/checkin'
import { useAppStore } from '@/stores'

vi.mock('@/api/checkin', () => ({
  checkinAPI: {
    getStatus: vi.fn(),
    checkin: vi.fn()
  }
}))

const mockedAPI = vi.mocked(checkinAPI)

function makeStatus(overrides: Record<string, unknown> = {}) {
  return {
    enabled: true,
    checked_in_today: false,
    today_record: null,
    current_streak: 2,
    milestone_cycle_day: 2,
    next_milestone: { days: 7, bonus_amount: 0.5 },
    milestones: [{ days: 7, bonus_amount: 0.5 }],
    milestone_cycle_repeat: false,
    reward_preview: {
      reward_mode: 'fixed' as const,
      base_amount: 0.1,
      min_amount: 0.05,
      max_amount: 0.2,
      amount_precision: 2,
      validity_days: 1
    },
    eligibility: { eligible: true },
    captcha_required: false,
    expiring_summary: {
      total_active: 0.2,
      frozen_amount: 0,
      nearest_expires_at: '2026-11-28T00:00:00Z',
      nearest_amount: 0.2
    },
    rules_description: '',
    server_date: '2026-11-27',
    timezone: 'Asia/Shanghai',
    ...overrides
  }
}

describe('UserDashboardCheckinCard', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  function mountCard() {
    const i18n = createI18n({
      legacy: false,
      locale: 'en',
      messages: { en: enLocale }
    })
    return mount(UserDashboardCheckinCard, {
      global: {
        plugins: [i18n],
        stubs: {
          RouterLink: { template: '<a><slot /></a>' },
          BaseDialog: {
            props: ['show'],
            template: '<div v-if="show"><slot /><slot name="footer" /></div>'
          },
          CaptchaChallenge: {
            name: 'CaptchaChallenge',
            template: '<div data-testid="mock-captcha-challenge" />',
            methods: {
              reset: vi.fn(),
              verifyAction: vi.fn().mockResolvedValue({ token: 'ticket-1', randstr: 'rand-1' })
            }
          }
        }
      }
    })
  }

  it('submits check-in directly when captcha is not required and emits checked-in', async () => {
    mockedAPI.getStatus
      .mockResolvedValueOnce(makeStatus())
      .mockResolvedValueOnce(
        makeStatus({
          checked_in_today: true,
          current_streak: 3,
          today_record: {
            id: 1,
            user_id: 1,
            checkin_date: '2026-11-27',
            streak_days: 3,
            milestone_cycle_day: 3,
            base_amount: 0.1,
            milestone_bonus_amount: 0,
            total_amount: 0.1,
            validity_days: 1,
            expires_at: '2026-11-28T00:00:00Z',
            created_at: '2026-11-27T08:00:00Z'
          }
        })
      )
    mockedAPI.checkin.mockResolvedValue({
      record: {
        id: 1,
        user_id: 1,
        checkin_date: '2026-11-27',
        streak_days: 3,
        milestone_cycle_day: 3,
        base_amount: 0.1,
        milestone_bonus_amount: 0,
        total_amount: 0.1,
        validity_days: 1,
        expires_at: '2026-11-28T00:00:00Z',
        created_at: '2026-11-27T08:00:00Z'
      },
      expiring_summary: { total_active: 0.3, frozen_amount: 0, nearest_amount: 0.2 },
      total_available_balance: 5.3
    })

    const wrapper = mountCard()
    await flushPromises()

    await wrapper.find('[data-testid="quick-checkin-btn"]').trigger('click')
    await flushPromises()

    expect(mockedAPI.checkin).toHaveBeenCalledWith(undefined)
    expect(wrapper.emitted('checkedIn')).toBeTruthy()
    expect(wrapper.text()).toContain('Checked in today')
  })

  it('opens captcha modal and sends Tencent proof when captcha_required is true', async () => {
    const appStore = useAppStore()
    appStore.$patch({
      publicSettingsLoaded: true,
      cachedPublicSettings: {
        tencent_captcha_enabled: true,
        tencent_captcha_app_id: '199999999'
      } as never
    })

    mockedAPI.getStatus.mockResolvedValue(makeStatus({ captcha_required: true }))
    mockedAPI.checkin.mockResolvedValue({
      record: {
        id: 2,
        user_id: 1,
        checkin_date: '2026-11-27',
        streak_days: 3,
        milestone_cycle_day: 3,
        base_amount: 0.1,
        milestone_bonus_amount: 0,
        total_amount: 0.1,
        validity_days: 1,
        expires_at: '2026-11-28T00:00:00Z',
        created_at: '2026-11-27T08:00:00Z'
      },
      expiring_summary: { total_active: 0.3, frozen_amount: 0, nearest_amount: 0.2 },
      total_available_balance: 5.3
    })

    const wrapper = mountCard()
    await flushPromises()

    await wrapper.find('[data-testid="quick-checkin-btn"]').trigger('click')
    await flushPromises()

    expect(mockedAPI.checkin).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="checkin-captcha-confirm"]').exists()).toBe(true)

    await wrapper.find('[data-testid="checkin-captcha-confirm"]').trigger('click')
    await flushPromises()

    expect(mockedAPI.checkin).toHaveBeenCalledWith({
      tencent_captcha_ticket: 'ticket-1',
      tencent_captcha_randstr: 'rand-1'
    })
  })

  it('closes dialog and syncs page data when the request fails but the server already committed', async () => {
    const appStore = useAppStore()
    appStore.$patch({
      publicSettingsLoaded: true,
      cachedPublicSettings: {
        tencent_captcha_enabled: true,
        tencent_captcha_app_id: '199999999'
      } as never
    })
    const successSpy = vi.spyOn(appStore, 'showSuccess')
    const errorSpy = vi.spyOn(appStore, 'showError')

    // 第一次：未签到。第二次（请求失败后重新拉取）：服务端已落库。
    mockedAPI.getStatus
      .mockResolvedValueOnce(makeStatus({ captcha_required: true }))
      .mockResolvedValue(
        makeStatus({
          captcha_required: true,
          checked_in_today: true,
          today_record: {
            id: 3,
            user_id: 1,
            checkin_date: '2026-11-27',
            streak_days: 3,
            milestone_cycle_day: 3,
            base_amount: 0.1,
            milestone_bonus_amount: 0,
            total_amount: 0.1,
            validity_days: 1,
            expires_at: '2026-11-28T00:00:00Z',
            created_at: '2026-11-27T08:00:00Z'
          }
        })
      )
    // 服务端已提交但客户端超时/网络抖动。
    mockedAPI.checkin.mockRejectedValue({ status: 0, message: 'timeout of 30000ms exceeded' })

    const wrapper = mountCard()
    await flushPromises()

    await wrapper.find('[data-testid="quick-checkin-btn"]').trigger('click')
    await flushPromises()

    const dialogShow = () => {
      const dialog = wrapper.findComponent({ name: 'CheckinCaptchaDialog' })
      return dialog.exists() ? (dialog.props('show') as boolean) : false
    }
    expect(dialogShow()).toBe(true)

    await wrapper.find('[data-testid="checkin-captcha-confirm"]').trigger('click')
    await flushPromises()

    // 回归点：请求失败但服务端已落库时，必须关闭弹窗、按成功提示并通知父组件刷新，
    // 而不是停在弹窗里显示一个误导性的错误。
    expect(dialogShow()).toBe(false)
    expect(successSpy).toHaveBeenCalled()
    expect(errorSpy).not.toHaveBeenCalled()
    expect(wrapper.emitted('checkedIn')).toBeTruthy()
    expect(wrapper.text()).toContain('Checked in today')
  })

  it('keeps the dialog open and reports the error when the server really did not commit', async () => {
    const appStore = useAppStore()
    appStore.$patch({
      publicSettingsLoaded: true,
      cachedPublicSettings: {
        tencent_captcha_enabled: true,
        tencent_captcha_app_id: '199999999'
      } as never
    })
    const errorSpy = vi.spyOn(appStore, 'showError')

    mockedAPI.getStatus.mockResolvedValue(makeStatus({ captcha_required: true }))
    mockedAPI.checkin.mockRejectedValue({
      status: 500,
      reason: 'CHECKIN_DAILY_BUDGET_EXHAUSTED',
      message: 'budget exhausted'
    })

    const wrapper = mountCard()
    await flushPromises()

    await wrapper.find('[data-testid="quick-checkin-btn"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-testid="checkin-captcha-confirm"]').trigger('click')
    await flushPromises()

    const dialogShow = () => {
      const dialog = wrapper.findComponent({ name: 'CheckinCaptchaDialog' })
      return dialog.exists() ? (dialog.props('show') as boolean) : false
    }
    // 真正失败时必须保留弹窗，让用户能重试。
    expect(dialogShow()).toBe(true)
    expect(errorSpy).toHaveBeenCalled()
    expect(wrapper.emitted('checkedIn')).toBeFalsy()
  })

  it('disables button and shows reason when user is ineligible', async () => {
    mockedAPI.getStatus.mockResolvedValue(
      makeStatus({
        eligibility: { eligible: false, reason_code: 'CHECKIN_INELIGIBLE_RECHARGE' }
      })
    )

    const wrapper = mountCard()
    await flushPromises()

    const btn = wrapper.find('[data-testid="quick-checkin-btn"]')
    expect((btn.element as HTMLButtonElement).disabled).toBe(true)
    expect(wrapper.find('[data-testid="checkin-card-ineligible"]').text()).toContain(
      'Total recharge amount does not meet the minimum requirement'
    )
  })

  it('displays 4-decimal reward amounts and toast without truncating when amount_precision is 4', async () => {
    const appStore = useAppStore()
    const successSpy = vi.spyOn(appStore, 'showSuccess')

    mockedAPI.getStatus
      .mockResolvedValueOnce(
        makeStatus({
          reward_preview: {
            reward_mode: 'random',
            base_amount: 0,
            min_amount: 0.001,
            max_amount: 0.005,
            amount_precision: 4,
            validity_days: 1
          }
        })
      )
      .mockResolvedValueOnce(
        makeStatus({
          checked_in_today: true,
          reward_preview: {
            reward_mode: 'random',
            base_amount: 0,
            min_amount: 0.001,
            max_amount: 0.005,
            amount_precision: 4,
            validity_days: 1
          },
          today_record: {
            id: 5,
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
          }
        })
      )
    mockedAPI.checkin.mockResolvedValue({
      record: {
        id: 5,
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
      expiring_summary: { total_active: 0.0025, frozen_amount: 0, nearest_amount: 0.0025 },
      total_available_balance: 0.0025
    })

    const wrapper = mountCard()
    await flushPromises()
    expect(wrapper.text()).toContain('$0.0010 ~ $0.0050')

    await wrapper.find('[data-testid="quick-checkin-btn"]').trigger('click')
    await flushPromises()

    expect(successSpy).toHaveBeenCalledWith(expect.stringContaining('$0.0025'))
    expect(wrapper.text()).toContain('+$0.0025')
  })

  it('computes wrap-around next milestone days when milestone_cycle_repeat is true', async () => {
    mockedAPI.getStatus.mockResolvedValue(
      makeStatus({
        checked_in_today: true,
        current_streak: 7,
        milestone_cycle_day: 7,
        milestone_cycle_repeat: true,
        milestones: [
          { days: 3, bonus_amount: 0.3 },
          { days: 7, bonus_amount: 0.5 }
        ],
        next_milestone: { days: 3, bonus_amount: 0.5 },
        today_record: {
          id: 9,
          user_id: 1,
          checkin_date: '2026-11-27',
          streak_days: 7,
          milestone_cycle_day: 7,
          base_amount: 0.1,
          milestone_bonus_amount: 0.5,
          total_amount: 0.6,
          validity_days: 1,
          expires_at: '2026-11-28T00:00:00Z',
          created_at: '2026-11-27T08:00:00Z'
        }
      })
    )

    const wrapper = mountCard()
    await flushPromises()

    expect(wrapper.text()).toContain('In 3d (+$0.50)')
    expect(wrapper.text()).not.toContain('In 0d')
  })
})
