import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import enLocale from '@/i18n/locales/en'
import DashboardView from '@/views/user/DashboardView.vue'
import { usageAPI } from '@/api/usage'
import { checkinAPI } from '@/api/checkin'
import { getMyPlatformQuotas } from '@/api/user'
import { useAppStore, useAuthStore } from '@/stores'

vi.mock('@/api/usage', () => ({
  usageAPI: {
    getDashboardStats: vi.fn(),
    getDashboardTrend: vi.fn(),
    getDashboardModels: vi.fn(),
    getByDateRange: vi.fn()
  }
}))
vi.mock('@/api/checkin', () => ({ checkinAPI: { getStatus: vi.fn(), checkin: vi.fn() } }))
vi.mock('@/api/user', () => ({ getMyPlatformQuotas: vi.fn() }))

const mockedUsage = vi.mocked(usageAPI)
const mockedCheckin = vi.mocked(checkinAPI)
const mockedQuotas = vi.mocked(getMyPlatformQuotas)

const checkinRecord = {
  id: 9,
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
}

function makeCheckinStatus(overrides: Record<string, unknown> = {}) {
  return {
    enabled: true,
    checked_in_today: false,
    today_record: null,
    current_streak: 1,
    milestone_cycle_day: 1,
    next_milestone: null,
    milestones: [],
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
    timezone: 'Asia/Shanghai',
    ...overrides
  }
}

describe('views/user/DashboardView check-in integration', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  function setupMocks(options: { captchaRequired: boolean }) {
    const appStore = useAppStore()
    const authStore = useAuthStore()
    appStore.$patch({
      publicSettingsLoaded: true,
      cachedPublicSettings: {
        checkin_enabled: true,
        tencent_captcha_enabled: true,
        tencent_captcha_app_id: '199999999'
      } as never
    })
    vi.spyOn(authStore, 'refreshUser').mockResolvedValue(null as never)

    mockedUsage.getDashboardStats.mockResolvedValue({
      total_actual_cost: 1,
      today_actual_cost: 0.1,
      by_platform: []
    } as never)
    mockedUsage.getDashboardTrend.mockResolvedValue({ trend: [] } as never)
    mockedUsage.getDashboardModels.mockResolvedValue({ models: [] } as never)
    mockedUsage.getByDateRange.mockResolvedValue({ items: [] } as never)
    mockedQuotas.mockResolvedValue({ platform_quotas: [] } as never)

    mockedCheckin.getStatus.mockResolvedValue(
      makeCheckinStatus({ captcha_required: options.captchaRequired }) as never
    )
    mockedCheckin.checkin.mockResolvedValue({
      record: checkinRecord,
      expiring_summary: { total_active: 0.5, frozen_amount: 0, nearest_amount: 0.5 },
      total_available_balance: 0.5
    } as never)
  }

  function mountDashboard() {
    const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: enLocale } })
    return mount(DashboardView, {
      attachTo: document.body,
      global: {
        plugins: [i18n],
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          UserDashboardCharts: true,
          UserDashboardRecentUsage: true,
          UserDashboardQuickActions: true,
          RouterLink: { template: '<a><slot /></a>' },
          CaptchaChallenge: {
            name: 'CaptchaChallenge',
            template: '<div />',
            methods: {
              reset: vi.fn(),
              verifyAction: vi.fn().mockResolvedValue({ token: 'tok', randstr: 'rand' })
            }
          }
        }
      }
    })
  }

  it('refreshes dashboard stats and platform quotas after a successful check-in', async () => {
    setupMocks({ captchaRequired: false })
    const wrapper = mountDashboard()
    await flushPromises()

    const statsCallsBefore = mockedUsage.getDashboardStats.mock.calls.length
    const quotasCallsBefore = mockedQuotas.mock.calls.length

    await wrapper.find('[data-testid="quick-checkin-btn"]').trigger('click')
    await flushPromises()

    expect(mockedCheckin.checkin).toHaveBeenCalledTimes(1)

    // 回归点：签到成功后必须重拉仪表盘统计与配额，否则余额/用量卡片停留在旧值。
    expect(mockedUsage.getDashboardStats.mock.calls.length).toBe(statsCallsBefore + 1)
    expect(mockedQuotas.mock.calls.length).toBe(quotasCallsBefore + 1)
    // 图表与近 30 天用量与签到无关，不应被额外触发。
    expect(mockedUsage.getDashboardTrend.mock.calls.length).toBe(1)
    expect(mockedUsage.getByDateRange.mock.calls.length).toBe(1)

    wrapper.unmount()
  })

  it('closes the captcha dialog after a successful captcha check-in', async () => {
    setupMocks({ captchaRequired: true })
    const wrapper = mountDashboard()
    await flushPromises()

    const dialogShow = () => {
      const dialog = wrapper.findComponent({ name: 'CheckinCaptchaDialog' })
      return dialog.exists() ? (dialog.props('show') as boolean) : false
    }

    await wrapper.find('[data-testid="quick-checkin-btn"]').trigger('click')
    await flushPromises()
    expect(dialogShow()).toBe(true)

    const confirmBtn = document.querySelector(
      '[data-testid="checkin-captcha-confirm"]'
    ) as HTMLButtonElement | null
    expect(confirmBtn).not.toBeNull()
    confirmBtn?.click()
    await flushPromises()

    expect(mockedCheckin.checkin).toHaveBeenCalledTimes(1)
    // 回归点：签到成功后弹窗的 show 必须变回 false（父组件是唯一真相，
    // 用 props 断言可避免 leave 过渡动画导致的时序误判）。
    expect(dialogShow()).toBe(false)

    // 等过渡结束后确认遮罩与滚动锁都已释放。
    await new Promise((resolve) => setTimeout(resolve, 350))
    expect(document.querySelectorAll('.modal-overlay')).toHaveLength(0)
    expect(document.body.classList.contains('modal-open')).toBe(false)

    wrapper.unmount()
  })
})
