import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import enLocale from '@/i18n/locales/en'
import PaymentView from '../PaymentView.vue'
import { useAppStore, useAuthStore, useSubscriptionStore } from '@/stores'

vi.mock('vue-router', () => ({
  useRoute: () => ({ query: { tab: 'subscription' }, path: '/purchase' }),
  useRouter: () => ({ push: vi.fn(), replace: vi.fn(), resolve: vi.fn(() => ({ href: '' })) })
}))

vi.mock('@/api/payment', () => ({
  paymentAPI: {
    getCheckoutInfo: vi.fn(),
    getOrder: vi.fn()
  }
}))

describe('PaymentView expiring balance integration', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('uses totalAvailableBalance for subscription purchase and shows FEFO/refund notice', async () => {
    const authStore = useAuthStore()
    const appStore = useAppStore()
    const subscriptionStore = useSubscriptionStore()

    appStore.$patch({
      publicSettingsLoaded: true,
      cachedPublicSettings: {
        payment_enabled: true,
        subscription_enabled: true
      } as never
    })
    authStore.$patch({
      token: 'tok',
      user: {
        id: 1,
        username: 'u1',
        email: 'u1@example.com',
        role: 'user',
        balance: 5,
        expiring_balance: 10,
        total_available_balance: 15,
        concurrency: 1,
        status: 'active',
        allowed_groups: null,
        balance_notify_enabled: false,
        balance_notify_threshold: null,
        balance_notify_extra_emails: [],
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-01T00:00:00Z'
      } as never
    })

    const { paymentAPI } = await import('@/api/payment')
    vi.mocked(paymentAPI.getCheckoutInfo).mockResolvedValue({
      data: {
        methods: {},
        global_min: 1,
        global_max: 1000,
        balance_disabled: false,
        balance_recharge_multiplier: 1,
        subscription_usd_to_cny_rate: 0,
        recharge_fee_rate: 0,
        help_text: '',
        help_image_url: '',
        stripe_publishable_key: '',
        plans: [
          {
            id: 1,
            name: 'Pro Plan',
            price: 12,
            validity_days: 30,
            group_id: 1,
            description: '',
            features: [],
            sort_order: 1,
            enabled: true,
            for_sale: true
          }
        ]
      }
    } as never)

    vi.spyOn(subscriptionStore, 'fetchActiveSubscriptions').mockResolvedValue([] as never)

    const i18n = createI18n({
      legacy: false,
      locale: 'en',
      messages: { en: enLocale }
    })

    const wrapper = mount(PaymentView, {
      global: {
        plugins: [i18n],
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          AmountInput: true,
          PaymentMethodSelector: {
            props: ['methods', 'selected'],
            emits: ['select'],
            template: '<button data-testid="select-balance-method" @click="$emit(\'select\', \'balance\')">balance</button>'
          },
          SubscriptionPlanCard: {
            props: ['plan', 'selected'],
            emits: ['select'],
            template: '<button data-testid="select-plan-btn" @click="$emit(\'select\', plan)">{{ plan.name }}</button>'
          },
          PaymentStatusPanel: true,
          BaseDialog: true,
          RouterLink: { template: '<a><slot /></a>' }
        }
      }
    })

    await flushPromises()

    await wrapper.find('[data-testid="select-plan-btn"]').trigger('click')
    await wrapper.find('[data-testid="select-balance-method"]').trigger('click')
    await flushPromises()

    const summary = wrapper.find('[data-testid="balance-payment-summary"]')
    expect(summary.exists()).toBe(true)
    expect(summary.text()).toContain('$15.00')
    expect(wrapper.find('[data-testid="balance-insufficient-notice"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="balance-expiring-notice"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="balance-expiring-notice"]').text()).toContain('FEFO')
  })
})
