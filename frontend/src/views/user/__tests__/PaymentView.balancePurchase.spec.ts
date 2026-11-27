import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { reactive } from 'vue'
import PaymentView from '../PaymentView.vue'
import PaymentMethodSelector from '@/components/payment/PaymentMethodSelector.vue'
import { formatPaymentAmount } from '@/components/payment/currency'
import type { CheckoutInfoResponse, MethodLimit, SubscriptionPlan } from '@/types/payment'

const routeState = vi.hoisted(() => ({
  path: '/purchase',
  query: {} as Record<string, unknown>,
}))

const routerReplace = vi.hoisted(() => vi.fn())
const routerPush = vi.hoisted(() => vi.fn())
const routerResolve = vi.hoisted(() => vi.fn(() => ({ href: '/payment/result' })))
const createOrder = vi.hoisted(() => vi.fn())
const refreshUser = vi.hoisted(() => vi.fn().mockResolvedValue(undefined))
const fetchActiveSubscriptions = vi.hoisted(() => vi.fn().mockResolvedValue(undefined))
const showError = vi.hoisted(() => vi.fn())
const showInfo = vi.hoisted(() => vi.fn())
const showWarning = vi.hoisted(() => vi.fn())
const showSuccess = vi.hoisted(() => vi.fn())
const getCheckoutInfo = vi.hoisted(() => vi.fn())

const authState = reactive({
  user: {
    username: 'balance-user',
    balance: 100,
  },
})

vi.mock('vue-router', async () => {
  const actual = await vi.importActual<typeof import('vue-router')>('vue-router')
  return {
    ...actual,
    useRoute: () => routeState,
    useRouter: () => ({
      replace: routerReplace,
      push: routerPush,
      resolve: routerResolve,
    }),
  }
})

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) =>
        params ? `${key}:${JSON.stringify(params)}` : key,
    }),
  }
})

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    get user() {
      return authState.user
    },
    refreshUser,
  }),
}))

vi.mock('@/stores/payment', () => ({
  usePaymentStore: () => ({
    createOrder,
  }),
}))

vi.mock('@/stores/subscriptions', () => ({
  useSubscriptionStore: () => ({
    activeSubscriptions: [],
    fetchActiveSubscriptions,
  }),
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    showError,
    showInfo,
    showWarning,
    showSuccess,
    cachedPublicSettings: { subscription_enabled: true },
  }),
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showInfo,
    showWarning,
    showSuccess,
    cachedPublicSettings: { subscription_enabled: true },
  }),
}))

vi.mock('@/api/payment', () => ({
  paymentAPI: {
    getCheckoutInfo,
  },
}))

vi.mock('@/utils/device', () => ({
  isMobileDevice: () => false,
}))

function samplePlan(overrides: Partial<SubscriptionPlan> = {}): SubscriptionPlan {
  return {
    id: 7,
    group_id: 3,
    name: 'Claude Pro Monthly',
    description: 'Monthly plan',
    price: 29,
    original_price: 0,
    validity_days: 30,
    validity_unit: 'days',
    rate_multiplier: 1,
    daily_limit_usd: 25,
    weekly_limit_usd: null,
    monthly_limit_usd: null,
    features: [],
    group_platform: 'anthropic',
    sort_order: 1,
    for_sale: true,
    group_name: 'Claude Standard',
    ...overrides,
  }
}

function checkoutFixture(overrides: Partial<CheckoutInfoResponse> = {}) {
  const wxpay: MethodLimit = {
    currency: 'CNY',
    daily_limit: 0,
    daily_used: 0,
    daily_remaining: 0,
    single_min: 0,
    single_max: 0,
    fee_rate: 0,
    available: true,
  }
  const data: CheckoutInfoResponse = {
    methods: { wxpay },
    global_min: 0,
    global_max: 0,
    plans: [samplePlan()],
    balance_disabled: false,
    balance_recharge_multiplier: 1,
    subscription_usd_to_cny_rate: 7.15,
    recharge_fee_rate: 2.5,
    help_text: '',
    help_image_url: '',
    stripe_publishable_key: '',
    ...overrides,
  }
  return { data }
}

function mountPaymentView() {
  return mount(PaymentView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        AmountInput: { template: '<div data-testid="amount-input-stub" />' },
        PaymentStatusPanel: { template: '<div data-testid="status-panel-stub" />' },
        Icon: { template: '<span />' },
        Teleport: true,
        Transition: false,
      },
    },
  })
}

describe('PaymentView subscription purchase with account balance', () => {
  beforeEach(() => {
    routeState.path = '/purchase'
    routeState.query = {}
    authState.user.balance = 100
    routerReplace.mockReset().mockResolvedValue(undefined)
    routerPush.mockReset().mockResolvedValue(undefined)
    createOrder.mockReset()
    refreshUser.mockReset().mockResolvedValue(undefined)
    fetchActiveSubscriptions.mockReset().mockResolvedValue(undefined)
    showError.mockReset()
    showInfo.mockReset()
    showWarning.mockReset()
    showSuccess.mockReset()
    window.localStorage.clear()
  })

  it('shows balance method only on subscription checkout, charges exact USD with 0 fee, and completes immediately', async () => {
    routeState.query = { tab: 'subscription', group: '3' }
    getCheckoutInfo.mockResolvedValue(checkoutFixture())
    createOrder.mockResolvedValue({
      order_id: 501,
      amount: 29,
      pay_amount: 29,
      fee_rate: 0,
      currency: 'USD',
      payment_type: 'balance',
      out_trade_no: 'sub2_bal_501',
      status: 'COMPLETED',
      result_type: 'completed',
      expires_at: '2099-01-01T00:00:00Z',
    })

    const wrapper = mountPaymentView()
    await flushPromises()
    await flushPromises()

    const selector = wrapper.getComponent(PaymentMethodSelector)
    const optionTypes = selector.props('methods').map((m) => m.type)
    expect(optionTypes).toEqual(['wxpay', 'balance'])

    // Select account balance payment method
    selector.vm.$emit('select', 'balance')
    await flushPromises()

    const summary = wrapper.get('[data-testid="balance-payment-summary"]')
    expect(summary.text()).toContain(formatPaymentAmount(100, 'USD'))
    expect(summary.text()).toContain(formatPaymentAmount(29, 'USD'))
    expect(summary.text()).toContain(formatPaymentAmount(71, 'USD'))
    // Fee row must be hidden when paying with balance
    expect(wrapper.text()).not.toContain('payment.actualPay')

    const payBtn = wrapper.findAll('button').find((b) => b.text().includes('payment.payWithBalance'))
    expect(payBtn).toBeDefined()
    expect(payBtn!.attributes('disabled')).toBeUndefined()
    await payBtn!.trigger('click')
    await flushPromises()

    expect(createOrder).toHaveBeenCalledWith(expect.objectContaining({
      amount: 29,
      payment_type: 'balance',
      order_type: 'subscription',
      plan_id: 7,
    }))
    expect(refreshUser).toHaveBeenCalled()
    expect(fetchActiveSubscriptions).toHaveBeenCalledWith(true)
    expect(showSuccess).toHaveBeenCalledWith('payment.balancePaySuccess')
    expect(routerPush).toHaveBeenCalledWith({
      path: '/payment/result',
      query: {
        order_id: '501',
        out_trade_no: 'sub2_bal_501',
      },
    })
  })

  it('auto-selects balance when no online payment gateways are configured and warns when balance is insufficient', async () => {
    authState.user.balance = 10
    routeState.query = { tab: 'subscription', group: '3' }
    getCheckoutInfo.mockResolvedValue(checkoutFixture({ methods: {} }))

    const wrapper = mountPaymentView()
    await flushPromises()
    await flushPromises()

    const selector = wrapper.getComponent(PaymentMethodSelector)
    expect(selector.props('methods')).toHaveLength(1)
    expect(selector.props('methods')[0].type).toBe('balance')
    expect(selector.props('methods')[0].available).toBe(false)
    expect(selector.props('selected')).toBe('balance')

    expect(wrapper.find('[data-testid="balance-insufficient-notice"]').exists()).toBe(true)
    const payBtn = wrapper.findAll('button').find((b) => b.text().includes('payment.payWithBalance'))
    expect(payBtn).toBeDefined()
    expect(payBtn!.attributes('disabled')).toBeDefined()

    // Clicking "go to recharge" navigates back to recharge tab without balance method
    await wrapper.get('[data-testid="go-to-recharge-btn"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('payment.rechargeAccount')
  })

  it('never shows balance method on the recharge tab and resets method when switching back from subscription', async () => {
    routeState.query = {}
    getCheckoutInfo.mockResolvedValue(checkoutFixture())

    const wrapper = mountPaymentView()
    await flushPromises()
    await flushPromises()

    // Recharge tab only has wxpay, never balance
    const rechargeSelector = wrapper.getComponent(PaymentMethodSelector)
    expect(rechargeSelector.props('methods').map((m) => m.type)).toEqual(['wxpay'])

    // Switch to subscription tab, pick a plan, select balance, then cancel and switch back to recharge
    const subTabBtn = wrapper.findAll('button').find((b) => b.text() === 'payment.tabSubscribe')
    await subTabBtn!.trigger('click')
    await flushPromises()

    const planBtn = wrapper.findAll('button').find((b) => b.text() === 'payment.subscribeNow')
    await planBtn!.trigger('click')
    await flushPromises()

    wrapper.getComponent(PaymentMethodSelector).vm.$emit('select', 'balance')
    await flushPromises()
    expect(wrapper.getComponent(PaymentMethodSelector).props('selected')).toBe('balance')

    const cancelBtn = wrapper.findAll('button').find((b) => b.text() === 'common.cancel')
    await cancelBtn!.trigger('click')
    await flushPromises()

    const topUpTabBtn = wrapper.findAll('button').find((b) => b.text() === 'payment.tabTopUp')
    await topUpTabBtn!.trigger('click')
    await flushPromises()

    const selectorAfter = wrapper.getComponent(PaymentMethodSelector)
    expect(selectorAfter.props('methods').map((m) => m.type)).toEqual(['wxpay'])
    expect(selectorAfter.props('selected')).toBe('wxpay')
  })
})
