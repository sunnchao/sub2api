import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import enLocale from '@/i18n/locales/en'
import PaymentView from '../PaymentView.vue'
import { useAppStore, useAuthStore, useSubscriptionStore } from '@/stores'

const { pushMock, routeQuery } = vi.hoisted(() => ({
  pushMock: vi.fn(),
  routeQuery: {} as Record<string, string>,
}))

vi.mock('vue-router', () => ({
  useRoute: () => ({ query: routeQuery, path: '/purchase' }),
  useRouter: () => ({ push: pushMock, replace: vi.fn(), resolve: vi.fn(() => ({ href: '' })) }),
}))

vi.mock('@/api/payment', () => ({
  paymentAPI: {
    getCheckoutInfo: vi.fn(),
    getOrder: vi.fn(),
  },
}))

function mountPaymentView() {
  const i18n = createI18n({
    legacy: false,
    locale: 'en',
    messages: { en: enLocale },
  })
  return mount(PaymentView, {
    global: {
      plugins: [i18n],
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        AmountInput: true,
        PaymentMethodSelector: true,
        SubscriptionPlanCard: true,
        PaymentStatusPanel: true,
        Teleport: true,
        Transition: false,
        Icon: true,
      },
    },
  })
}

function setupUser() {
  const authStore = useAuthStore()
  authStore.$patch({
    token: 'tok',
    user: {
      id: 1,
      username: 'alice',
      email: 'alice@example.com',
      role: 'user',
      balance: 25,
      concurrency: 2,
      status: 'active',
    } as never,
  })
}

describe('PaymentView redeem store entry', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    Object.keys(routeQuery).forEach((k) => delete routeQuery[k])
    setupUser()
  })

  it('does not render redeem store entry when redeem_store_url is empty', async () => {
    const appStore = useAppStore()
    const subscriptionStore = useSubscriptionStore()
    appStore.$patch({
      publicSettingsLoaded: true,
      cachedPublicSettings: {
        payment_enabled: true,
        subscription_enabled: true,
        redeem_store_url: '',
      } as never,
    })
    vi.spyOn(subscriptionStore, 'fetchActiveSubscriptions').mockResolvedValue([] as never)

    const { paymentAPI } = await import('@/api/payment')
    vi.mocked(paymentAPI.getCheckoutInfo).mockResolvedValue({
      data: {
        methods: { alipay: { single_min: 1, single_max: 1000, fee_rate: 0 } },
        global_min: 1,
        global_max: 1000,
        plans: [],
        balance_disabled: false,
        balance_recharge_multiplier: 1,
        subscription_usd_to_cny_rate: 0,
        recharge_fee_rate: 0,
        help_text: '',
        help_image_url: '',
        stripe_publishable_key: '',
      },
    } as never)

    const wrapper = mountPaymentView()
    await flushPromises()
    expect(wrapper.find('[data-testid="payment-redeem-store-entry"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('shows redeem store entry on recharge tab (including empty methods) and hides it on subscription tab', async () => {
    const appStore = useAppStore()
    const subscriptionStore = useSubscriptionStore()
    appStore.$patch({
      publicSettingsLoaded: true,
      cachedPublicSettings: {
        payment_enabled: true,
        subscription_enabled: true,
        redeem_store_url: 'https://shop.example.com/cards',
        redeem_store_open_mode: 'new_tab',
        redeem_store_label: 'Official Card Shop',
      } as never,
    })
    vi.spyOn(subscriptionStore, 'fetchActiveSubscriptions').mockResolvedValue([] as never)

    const { paymentAPI } = await import('@/api/payment')
    vi.mocked(paymentAPI.getCheckoutInfo).mockResolvedValue({
      data: {
        methods: {},
        global_min: 0,
        global_max: 0,
        plans: [],
        balance_disabled: false,
        balance_recharge_multiplier: 1,
        subscription_usd_to_cny_rate: 0,
        recharge_fee_rate: 0,
        help_text: '',
        help_image_url: '',
        stripe_publishable_key: '',
      },
    } as never)

    const wrapper = mountPaymentView()
    await flushPromises()

    // Shown on recharge tab even with 0 enabled payment methods
    const entry = wrapper.get('[data-testid="payment-redeem-store-entry"]')
    const link = entry.get('[data-testid="payment-redeem-store-link"]')
    expect(link.attributes('href')).toBe('https://shop.example.com/cards')
    expect(link.text()).toContain('Official Card Shop')

    await entry.get('[data-testid="payment-go-redeem-link"]').trigger('click')
    expect(pushMock).toHaveBeenCalledWith('/redeem')

    // Switch to subscription tab -> entry is hidden
    const subTabBtn = wrapper.findAll('button').find((b) => b.text() === 'Subscribe')!
    await subTabBtn.trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="payment-redeem-store-entry"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('renders redeem-store-only guidance and skips getCheckoutInfo when payment_enabled is false', async () => {
    const appStore = useAppStore()
    appStore.$patch({
      publicSettingsLoaded: true,
      cachedPublicSettings: {
        payment_enabled: false,
        subscription_enabled: true,
        redeem_store_url: 'https://shop.example.com/cards',
        redeem_store_open_mode: 'iframe',
        redeem_store_label: 'Buy Card',
      } as never,
    })

    const { paymentAPI } = await import('@/api/payment')

    const wrapper = mountPaymentView()
    await flushPromises()

    expect(paymentAPI.getCheckoutInfo).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="payment-redeem-store-only-card"]').exists()).toBe(true)

    await wrapper.get('[data-testid="payment-redeem-store-modal-btn"]').trigger('click')
    const iframe = wrapper.get('[data-testid="redeem-store-iframe"]')
    expect(iframe.attributes('src')).toBe('https://shop.example.com/cards')

    await wrapper.get('[data-testid="redeem-store-modal-go-redeem"]').trigger('click')
    expect(pushMock).toHaveBeenCalledWith('/redeem')
    wrapper.unmount()
  })
})
