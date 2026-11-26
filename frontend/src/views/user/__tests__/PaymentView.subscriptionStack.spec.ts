import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import SubscriptionPlanCard from '@/components/payment/SubscriptionPlanCard.vue'
import { buildCreateOrderPayload } from '@/components/payment/paymentFlow'
import type { SubscriptionPlan } from '@/types/payment'
import type { UserSubscription } from '@/types'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (k: string) => k })
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ cachedPublicSettings: null })
}))

describe('Subscription stacking vs renewal checkout flow', () => {
  const plan: SubscriptionPlan = {
    id: 11,
    group_id: 10,
    template_id: 5,
    template_name: 'Claude Pro',
    group_platform: 'anthropic',
    group_name: 'Claude Standard',
    rate_multiplier: 1,
    daily_limit_usd: 20,
    weekly_limit_usd: null,
    monthly_limit_usd: 300,
    hourly_limit_usd: null,
    hourly_window_hours: 5,
    name: 'Claude Pro 30d',
    description: 'Stackable plan',
    price: 29,
    validity_days: 30,
    validity_unit: 'days',
    features: [],
    for_sale: true,
    sort_order: 1
  }

  const ownedSub = {
    id: 88,
    user_id: 1,
    group_id: 10,
    template_id: 5,
    template_name: 'Claude Pro',
    status: 'active',
    starts_at: '2026-01-01T00:00:00Z',
    expires_at: '2026-02-01T00:00:00Z',
    daily_usage_usd: 0,
    weekly_usage_usd: 0,
    monthly_usage_usd: 0,
    hourly_usage_usd: 0,
    daily_window_start: null,
    weekly_window_start: null,
    monthly_window_start: null,
    hourly_window_start: null,
    created_at: '',
    updated_at: ''
  } as UserSubscription

  it('SubscriptionPlanCard defaults button to subscribeNow (stack) and displays owned badge', () => {
    const wrapper = mount(SubscriptionPlanCard, {
      props: { plan, activeSubscriptions: [ownedSub] }
    })

    expect(wrapper.find('[data-testid="plan-owned-badge"]').exists()).toBe(true)
    expect(wrapper.get('button').text()).toBe('payment.subscribeNow')
  })

  it('buildCreateOrderPayload omits target_subscription_id for stack mode and includes it for renew mode', () => {
    const stackPayload = buildCreateOrderPayload({
      amount: 29,
      paymentType: 'alipay',
      orderType: 'subscription',
      planId: 11,
      origin: 'https://example.com',
      isMobile: false,
      isWechatBrowser: false
    })
    expect(stackPayload.target_subscription_id).toBeUndefined()

    const renewPayload = buildCreateOrderPayload({
      amount: 29,
      paymentType: 'alipay',
      orderType: 'subscription',
      planId: 11,
      targetSubscriptionId: 88,
      origin: 'https://example.com',
      isMobile: false,
      isWechatBrowser: false
    })
    expect(renewPayload.target_subscription_id).toBe(88)
  })
})
