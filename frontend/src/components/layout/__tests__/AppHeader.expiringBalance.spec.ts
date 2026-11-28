import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import enLocale from '@/i18n/locales/en'
import AppHeader from '../AppHeader.vue'
import { useAppStore, useAuthStore } from '@/stores'

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn() }),
  useRoute: () => ({ name: 'Dashboard', path: '/dashboard', meta: { title: 'Dashboard' }, params: {} })
}))

describe('AppHeader expiring balance display', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  function mountHeader(userPatch: Record<string, unknown>) {
    const authStore = useAuthStore()
    const appStore = useAppStore()
    appStore.$patch({
      publicSettingsLoaded: true,
      cachedPublicSettings: {
        site_name: 'Sub2API',
        model_plaza_enabled: false,
        subscription_enabled: false
      } as never
    })
    authStore.$patch({
      token: 'tok',
      user: {
        id: 1,
        username: 'alice',
        email: 'alice@example.com',
        role: 'user',
        balance: 10,
        frozen_balance: 1,
        concurrency: 1,
        status: 'active',
        allowed_groups: null,
        balance_notify_enabled: false,
        balance_notify_threshold: null,
        balance_notify_extra_emails: [],
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-01T00:00:00Z',
        ...userPatch
      } as never
    })

    const i18n = createI18n({
      legacy: false,
      locale: 'en',
      messages: { en: enLocale }
    })

    return mount(AppHeader, {
      global: {
        plugins: [i18n],
        stubs: {
          LocaleSwitcher: true,
          SubscriptionProgressMini: true,
          AnnouncementBell: true,
          RouterLink: { template: '<a><slot /></a>' }
        }
      }
    })
  }

  it('displays total available balance, expiring badge, and breakdown when expiring_balance > 0', () => {
    const wrapper = mountHeader({
      expiring_balance: 2.5,
      total_available_balance: 12.5,
      expiring_frozen_balance: 0.5,
      nearest_expiring_at: '2026-11-27T23:59:59Z'
    })

    expect(wrapper.text()).toContain('$12.50')
    expect(wrapper.find('[data-testid="header-expiring-badge"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="header-expiring-badge"]').text()).toContain('$2.50')
    expect(wrapper.text()).toContain('Regular balance')
    expect(wrapper.text()).toContain('$10.00')
    expect(wrapper.text()).toContain('Expiring balance')
    expect(wrapper.text()).toContain('$1.50') // combined frozen: 1 + 0.5
    expect(wrapper.text()).toContain('$14.00') // total: 12.5 + 1.5
  })

  it('hides expiring badge when expiring_balance is 0', () => {
    const wrapper = mountHeader({
      expiring_balance: 0,
      total_available_balance: 10
    })

    expect(wrapper.text()).toContain('$10.00')
    expect(wrapper.find('[data-testid="header-expiring-badge"]').exists()).toBe(false)
  })
})
