import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import SubscriptionsView from '../SubscriptionsView.vue'

const routerPush = vi.hoisted(() => vi.fn())
const getMySubscriptions = vi.hoisted(() => vi.fn())

vi.mock('vue-router', async () => {
  const actual = await vi.importActual<typeof import('vue-router')>('vue-router')
  return {
    ...actual,
    useRouter: () => ({ push: routerPush }),
  }
})

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    cachedPublicSettings: null,
  }),
}))

vi.mock('@/api/subscriptions', () => ({
  default: {
    getMySubscriptions,
  },
}))

describe('SubscriptionsView view usage details button', () => {
  beforeEach(() => {
    routerPush.mockReset()
    getMySubscriptions.mockReset()
  })

  it('navigates to /usage filtered by subscription_id and billing_type=1 when clicking View Details', async () => {
    getMySubscriptions.mockResolvedValue([
      {
        id: 77,
        user_id: 1,
        group_id: 5,
        template_id: 3,
        template_name: 'Token Plan Pro',
        has_custom_limits: true,
        hourly_limit_usd: 18,
        hourly_window_hours: 5,
        hourly_usage_usd: 2,
        weekly_limit_usd: 144,
        weekly_usage_usd: 10,
        monthly_limit_usd: 288,
        monthly_usage_usd: 20,
        status: 'active',
        starts_at: '2026-11-01T00:00:00Z',
        expires_at: '2099-12-01T00:00:00Z',
        group: { id: 5, name: 'Composite', platform: 'composite', rate_multiplier: 1 },
      },
    ])

    const wrapper = mount(SubscriptionsView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          Icon: true,
        },
      },
    })
    await flushPromises()

    const detailsBtn = wrapper.get('[data-testid="subscription-view-details-btn"]')
    expect(detailsBtn.text()).toBe('userSubscriptions.viewDetails')
    await detailsBtn.trigger('click')

    expect(routerPush).toHaveBeenCalledWith({
      path: '/usage',
      query: {
        subscription_id: '77',
        billing_type: '1',
      },
    })
  })
})
