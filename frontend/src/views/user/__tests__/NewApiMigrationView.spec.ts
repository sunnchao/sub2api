import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import NewApiMigrationView from '../NewApiMigrationView.vue'

const mockGetInfo = vi.hoisted(() => vi.fn())
const mockPreview = vi.hoisted(() => vi.fn())
const mockExecute = vi.hoisted(() => vi.fn())
const mockListRecords = vi.hoisted(() => vi.fn())
const mockRefreshUser = vi.hoisted(() => vi.fn())
const mockShowSuccess = vi.hoisted(() => vi.fn())
const mockShowError = vi.hoisted(() => vi.fn())

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    user: { id: 10, email: 'alice@sub2api.test', balance: 2.5 },
    refreshUser: mockRefreshUser
  })
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showSuccess: mockShowSuccess,
    showError: mockShowError
  })
}))

vi.mock('@/api/newApiMigration', () => ({
  newApiMigrationAPI: {
    getInfo: mockGetInfo,
    preview: mockPreview,
    execute: mockExecute,
    listRecords: mockListRecords
  }
}))

describe('NewApiMigrationView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockGetInfo.mockResolvedValue({
      enabled: true,
      migrate_from_enabled: true,
      migrate_to_enabled: false,
      site_name: 'My NewAPI',
      site_url: 'https://newapi.example.com',
      quota_per_unit: 500000,
      exchange_rate: 1.0
    })
    mockListRecords.mockResolvedValue({
      items: [],
      total: 0,
      page: 1,
      page_size: 20,
      pages: 0
    })
    mockRefreshUser.mockResolvedValue({ id: 10, balance: 12.5 })
  })

  it('previews regular & gift quota and executes one-time full migration after confirmation', async () => {
    mockPreview.mockResolvedValue({
      site_url: 'https://newapi.example.com',
      site_name: 'My NewAPI',
      new_api_user_id: 42,
      new_api_username: 'alice',
      new_api_display_name: 'Alice',
      new_api_email: 'alice@newapi.test',
      new_api_role: 1,
      new_api_status: 1,
      regular_quota: 5000000,
      gift_quota: 1000000,
      total_quota: 6000000,
      used_quota: 100000,
      quota_per_unit: 500000,
      exchange_rate: 1.0,
      raw_usd: 10,
      estimated_credit: 10,
      current_balance: 2.5,
      estimated_new_balance: 12.5,
      already_migrated: false,
      can_migrate: true
    })

    mockExecute.mockResolvedValue({
      new_balance: 12.5,
      record: {
        id: 1,
        direction: 'from_new_api',
        status: 'completed',
        site_url: 'https://newapi.example.com',
        sub2api_user_id: 10,
        sub2api_user_email: 'alice@sub2api.test',
        new_api_user_id: 42,
        new_api_username: 'alice',
        new_api_display_name: 'Alice',
        new_api_email: 'alice@newapi.test',
        new_api_quota_before: 5000000,
        new_api_quota_delta: 5000000,
        new_api_quota_after: 0,
        new_api_gift_quota: 1000000,
        quota_per_unit: 500000,
        exchange_rate: 1,
        sub2api_balance_before: 2.5,
        sub2api_balance_delta: 10,
        sub2api_balance_after: 12.5,
        created_at: '2026-11-19T00:00:00Z',
        updated_at: '2026-11-19T00:00:00Z'
      }
    })

    const wrapper = mount(NewApiMigrationView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          Icon: true,
          Pagination: true,
          ConfirmDialog: {
            props: ['show'],
            emits: ['confirm', 'cancel'],
            template: '<button v-if="show" data-test="confirm-btn" @click="$emit(\'confirm\')">confirm</button>'
          }
        }
      }
    })

    await flushPromises()

    const tokenInput = wrapper.find('#new-api-access-token')
    await tokenInput.setValue('nap_user_alice')
    await wrapper.find('form').trigger('submit.prevent')
    await flushPromises()

    expect(mockPreview).toHaveBeenCalledWith('nap_user_alice')
    expect(wrapper.text()).toContain('newApiMigration.user.giftQuotaNotice')
    expect(wrapper.text()).toContain('newApiMigration.user.oneTimeWarning')

    // Click confirm migration button to open dialog, then confirm
    const openConfirmBtn = wrapper
      .findAll('button')
      .find((b) => b.text().includes('newApiMigration.user.confirmButton'))
    expect(openConfirmBtn).toBeDefined()
    await openConfirmBtn!.trigger('click')
    await wrapper.find('[data-test="confirm-btn"]').trigger('click')
    await flushPromises()

    expect(mockExecute).toHaveBeenCalledWith('nap_user_alice', 42)
    expect(mockRefreshUser).toHaveBeenCalled()
    expect(mockShowSuccess).toHaveBeenCalledWith('newApiMigration.user.successTitle')
    expect(wrapper.text()).toContain('newApiMigration.user.successTitle')
  })

  it('renders disabled notice when migration is turned off', async () => {
    mockGetInfo.mockResolvedValue({
      enabled: false,
      migrate_from_enabled: false,
      migrate_to_enabled: false,
      site_name: 'New API',
      site_url: '',
      quota_per_unit: 500000,
      exchange_rate: 1.0
    })

    const wrapper = mount(NewApiMigrationView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          Icon: true,
          Pagination: true,
          ConfirmDialog: true
        }
      }
    })

    await flushPromises()
    expect(wrapper.text()).toContain('newApiMigration.user.disabledTitle')
    expect(wrapper.find('#new-api-access-token').exists()).toBe(false)
  })
})
