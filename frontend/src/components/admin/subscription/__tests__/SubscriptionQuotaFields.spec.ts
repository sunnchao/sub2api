import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import SubscriptionQuotaFields from '../SubscriptionQuotaFields.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) =>
      params?.hours ? `${key}:${params.hours}` : key
  })
}))

describe('SubscriptionQuotaFields', () => {
  it('renders 4 limit inputs + hourly window with custom testIdPrefix and emits numeric / null updates', async () => {
    const wrapper = mount(SubscriptionQuotaFields, {
      props: {
        dailyLimitUsd: 10,
        weeklyLimitUsd: null,
        monthlyLimitUsd: 100,
        hourlyLimitUsd: 2.5,
        hourlyWindowHours: 4,
        testIdPrefix: 'create'
      }
    })

    const dailyInput = wrapper.get('[data-testid="create-daily-limit"]')
    const hourlyInput = wrapper.get('[data-testid="create-hourly-limit"]')
    const hoursInput = wrapper.get('[data-testid="create-hourly-window-hours"]')

    expect((dailyInput.element as HTMLInputElement).value).toBe('10')
    expect((hourlyInput.element as HTMLInputElement).value).toBe('2.5')
    expect((hoursInput.element as HTMLInputElement).value).toBe('4')
    expect(wrapper.text()).toContain('admin.groups.subscription.hourlyHint:4')

    // Clearing daily limit emits null (unlimited)
    await dailyInput.setValue('')
    expect(wrapper.emitted('update:dailyLimitUsd')?.[0]).toEqual([null])

    // Setting weekly limit emits number
    const weeklyInput = wrapper.get('[data-testid="create-weekly-limit"]')
    await weeklyInput.setValue('35.5')
    expect(wrapper.emitted('update:weeklyLimitUsd')?.[0]).toEqual([35.5])

    // Updating hourly window hours emits parsed integer
    await hoursInput.setValue('8')
    expect(wrapper.emitted('update:hourlyWindowHours')?.[0]).toEqual([8])
  })
})
