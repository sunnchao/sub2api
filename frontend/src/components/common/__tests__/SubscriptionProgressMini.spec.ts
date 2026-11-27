import { enableAutoUnmount, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import SubscriptionProgressMini from '../SubscriptionProgressMini.vue'
const store = vi.hoisted(() => ({ activeSubscriptions: [] as unknown[], hasActiveSubscriptions: true, fetchActiveSubscriptions: vi.fn().mockResolvedValue(undefined) }))
vi.mock('@/stores', () => ({ useSubscriptionStore: () => store }))
vi.mock('@/utils/featureFlags', () => ({ FeatureFlags: { subscription: 'subscription' }, isFeatureFlagEnabled: () => true }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)
beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(new Date(2026, 8, 22, 12)) })
afterEach(() => vi.useRealTimers())
describe('subscription expiry calendar labels', () => {
  it.each([
    [new Date(2026, 8, 22, 18), 'expiresToday'],
    [new Date(2026, 8, 23, 18), 'expiresTomorrow'],
    [new Date(2026, 8, 22, 12), 'expired'],
    [new Date(2026, 8, 25, 12), 'daysRemaining'],
  ])('labels %s as %s', async (expires, label) => {
    store.activeSubscriptions = [{ id: 1, group_id: 1, expires_at: expires.toISOString(), group: { name: 'Plan' } }]
    const w = mount(SubscriptionProgressMini, { global: { stubs: { Icon: true, RouterLink: true } } })
    await w.get('button').trigger('click')
    expect(w.text()).toContain('subscriptionProgress.' + label)
  })

  it('shows hourly limit before daily, weekly, and monthly limits including custom template snapshots', async () => {
    const expires = new Date(2026, 8, 25, 12)
    store.activeSubscriptions = [{
      id: 2,
      group_id: 1,
      template_name: 'Token Plan Pro',
      has_custom_limits: true,
      hourly_limit_usd: 18,
      hourly_window_hours: 5,
      hourly_usage_usd: 2,
      daily_limit_usd: 50,
      daily_usage_usd: 5,
      weekly_limit_usd: 144,
      weekly_usage_usd: 10,
      monthly_limit_usd: 288,
      monthly_usage_usd: 20,
      expires_at: expires.toISOString(),
      group: { name: 'Standard' },
    }]
    const w = mount(SubscriptionProgressMini, { global: { stubs: { Icon: true, RouterLink: true } } })
    await w.get('button').trigger('click')
    const text = w.text()
    expect(text).toContain('Token Plan Pro')
    const hourlyIdx = text.indexOf('subscriptionProgress.hourly')
    const dailyIdx = text.indexOf('subscriptionProgress.daily')
    const weeklyIdx = text.indexOf('subscriptionProgress.weekly')
    const monthlyIdx = text.indexOf('subscriptionProgress.monthly')
    expect(hourlyIdx).toBeGreaterThan(-1)
    expect(dailyIdx).toBeGreaterThan(hourlyIdx)
    expect(weeklyIdx).toBeGreaterThan(dailyIdx)
    expect(monthlyIdx).toBeGreaterThan(weeklyIdx)
  })
})
