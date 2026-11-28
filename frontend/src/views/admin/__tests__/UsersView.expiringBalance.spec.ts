import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import enLocale from '@/i18n/locales/en'
import UserBalanceHistoryModal from '@/components/admin/user/UserBalanceHistoryModal.vue'
import { adminAPI } from '@/api/admin'

vi.mock('@/api/admin', () => ({
  adminAPI: {
    users: {
      getUserBalanceHistory: vi.fn().mockResolvedValue({
        items: [],
        total: 0,
        total_recharged: 0
      })
    }
  }
}))

describe('Admin user read-only expiring balance display', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('shows read-only expiring balance in UserBalanceHistoryModal when expiring_balance > 0', async () => {
    const i18n = createI18n({
      legacy: false,
      locale: 'en',
      messages: { en: enLocale }
    })

    const wrapper = mount(UserBalanceHistoryModal, {
      props: {
        show: false,
        user: {
          id: 1,
          username: 'u1',
          email: 'u1@example.com',
          role: 'user',
          balance: 20,
          expiring_balance: 3.5,
          nearest_expiring_at: '2026-11-27T23:59:59Z',
          concurrency: 1,
          status: 'active',
          allowed_groups: null,
          balance_notify_enabled: false,
          balance_notify_threshold: null,
          balance_notify_extra_emails: [],
          notes: '',
          created_at: '2026-01-01T00:00:00Z',
          updated_at: '2026-01-01T00:00:00Z'
        } as never
      },
      global: {
        plugins: [i18n],
        stubs: {
          BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' }
        }
      }
    })
    await wrapper.setProps({ show: true })
    await flushPromises()

    expect(adminAPI.users.getUserBalanceHistory).toHaveBeenCalled()
    expect(wrapper.text()).toContain('$20.00')
    const expiringEl = wrapper.find('[data-testid="history-modal-expiring-balance"]')
    expect(expiringEl.exists()).toBe(true)
    expect(expiringEl.text()).toContain('3.50')

    await wrapper.setProps({
      user: {
        ...(wrapper.props('user') as object),
        expiring_balance: 0
      } as never
    })
    expect(wrapper.find('[data-testid="history-modal-expiring-balance"]').exists()).toBe(false)
  })
})
