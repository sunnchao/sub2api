import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { reactive } from 'vue'
import RedeemView from '../RedeemView.vue'

const { getHistory, getPublicSettings } = vi.hoisted(() => ({
  getHistory: vi.fn(),
  getPublicSettings: vi.fn(),
}))

const appStoreState = reactive({
  cachedPublicSettings: null as Record<string, unknown> | null,
  showError: vi.fn(),
  showWarning: vi.fn(),
  showSuccess: vi.fn(),
})

vi.mock('@/api', () => ({
  redeemAPI: { redeem: vi.fn(), getHistory },
  authAPI: { getPublicSettings },
}))
vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ user: { balance: 10, concurrency: 2 }, refreshUser: vi.fn() }),
}))
vi.mock('@/stores/subscriptions', () => ({
  useSubscriptionStore: () => ({ fetchActiveSubscriptions: vi.fn() }),
}))
vi.mock('@/stores/app', () => ({
  useAppStore: () => appStoreState,
}))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

function mountView() {
  return mount(RedeemView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        Icon: true,
        Teleport: true,
        Transition: false,
      },
    },
  })
}

describe('RedeemView redeem store entry', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    appStoreState.cachedPublicSettings = null
    getHistory.mockResolvedValue({ items: [], total: 0 })
    getPublicSettings.mockResolvedValue({})
  })

  it('hides redeem store entry when redeem_store_url is not set', async () => {
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.find('[data-testid="redeem-store-entry"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('renders external new-tab link with custom label when configured', async () => {
    getPublicSettings.mockResolvedValue({
      redeem_store_url: 'https://shop.example.com/cards',
      redeem_store_open_mode: 'new_tab',
      redeem_store_label: '前往官方发卡站',
    })
    const wrapper = mountView()
    await flushPromises()

    const link = wrapper.get('[data-testid="redeem-store-link"]')
    expect(link.attributes('href')).toBe('https://shop.example.com/cards')
    expect(link.attributes('target')).toBe('_blank')
    expect(link.attributes('rel')).toBe('noopener noreferrer')
    expect(link.text()).toContain('前往官方发卡站')
    wrapper.unmount()
  })

  it('opens in-app iframe modal when open mode is iframe', async () => {
    getPublicSettings.mockResolvedValue({
      redeem_store_url: 'https://shop.example.com/cards',
      redeem_store_open_mode: 'iframe',
      redeem_store_label: '',
    })
    const wrapper = mountView()
    await flushPromises()

    const btn = wrapper.get('[data-testid="redeem-store-modal-btn"]')
    expect(btn.text()).toContain('redeem.buyRedeemCode')
    expect(wrapper.find('[data-testid="redeem-store-modal"]').exists()).toBe(false)

    await btn.trigger('click')
    const iframe = wrapper.get('[data-testid="redeem-store-iframe"]')
    expect(iframe.attributes('src')).toBe('https://shop.example.com/cards')
    wrapper.unmount()
  })
})
