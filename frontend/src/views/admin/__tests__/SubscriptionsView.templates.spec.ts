import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import SubscriptionsView from '../SubscriptionsView.vue'

const { listSubsMock, listGroupsMock, listTemplatesMock, updateLimitsMock, assignMock } = vi.hoisted(() => ({
  listSubsMock: vi.fn(),
  listGroupsMock: vi.fn(),
  listTemplatesMock: vi.fn(),
  updateLimitsMock: vi.fn(),
  assignMock: vi.fn()
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (k: string) => k }),
  createI18n: () => ({ global: { t: (k: string) => k } })
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showSuccess: vi.fn(), showError: vi.fn() })
}))

vi.mock('@/api/admin', () => {
  const api = {
    subscriptions: {
      list: listSubsMock,
      assign: assignMock,
      bulkAssign: vi.fn(),
      updateLimits: updateLimitsMock,
      extend: vi.fn(),
      revoke: vi.fn(),
      restore: vi.fn(),
      resetQuota: vi.fn()
    },
    groups: { getAll: listGroupsMock },
    subscriptionTemplates: { list: listTemplatesMock },
    usage: { searchUsers: vi.fn().mockResolvedValue([]) },
    users: { list: vi.fn().mockResolvedValue({ items: [] }) }
  }
  return { default: api, adminAPI: api }
})

const DataTableStub = defineComponent({
  name: 'DataTable',
  props: { data: { type: Array, default: () => [] } },
  template: `
    <div>
      <div v-for="row in data" :key="row.id" :data-sub-row="row.id">
        <slot name="cell-group" :row="row" />
        <slot name="cell-usage" :row="row" />
        <slot name="cell-actions" :row="row" />
      </div>
    </div>
  `
})

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: { show: Boolean },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>'
})

describe('SubscriptionsView templates & snapshot limits', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    listGroupsMock.mockResolvedValue([
      {
        id: 10,
        name: 'Claude Standard',
        platform: 'anthropic',
        rate_multiplier: 1,
        status: 'active',
        subscription_type: 'standard',
        daily_limit_usd: 99
      }
    ])
    listTemplatesMock.mockResolvedValue([
      {
        id: 5,
        name: 'Claude Starter',
        group_id: 10,
        daily_limit_usd: 15,
        default_validity_days: 30,
        status: 'active'
      }
    ])
    listSubsMock.mockResolvedValue({
      items: [
        {
          id: 101,
          user_id: 7,
          group_id: 10,
          status: 'active',
          starts_at: '2026-01-01T00:00:00Z',
          expires_at: '2026-02-01T00:00:00Z',
          daily_usage_usd: 3,
          weekly_usage_usd: 0,
          monthly_usage_usd: 0,
          hourly_usage_usd: 0,
          daily_window_start: null,
          weekly_window_start: null,
          monthly_window_start: null,
          hourly_window_start: null,
          template_id: 5,
          template_name: 'Claude Starter',
          has_custom_limits: true,
          daily_limit_usd: 15,
          weekly_limit_usd: null,
          monthly_limit_usd: null,
          hourly_limit_usd: null,
          hourly_window_hours: 5,
          group: {
            id: 10,
            name: 'Claude Standard',
            platform: 'anthropic',
            subscription_type: 'standard',
            rate_multiplier: 1,
            daily_limit_usd: 99
          }
        }
      ],
      total: 1,
      pages: 1
    })
    updateLimitsMock.mockResolvedValue({ id: 101 })
  })

  it('shows dual tabs, renders snapshotted limits instead of group defaults, and opens single-sub limit editor', async () => {
    const wrapper = mount(SubscriptionsView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          TablePageLayout: { template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>' },
          DataTable: DataTableStub,
          BaseDialog: BaseDialogStub,
          ConfirmDialog: true,
          Pagination: true,
          EmptyState: true,
          Select: true,
          GroupBadge: { props: ['name'], template: '<span>{{ name }}</span>' },
          GroupOptionItem: true,
          BulkSubscriptionActionDialog: true,
          SubscriptionTemplatesTab: { template: '<div data-test="templates-tab-panel">templates</div>' },
          Icon: true,
          RouterLink: true
        }
      }
    })
    await flushPromises()

    // Displays template badge & snapshotted $15 limit (NOT group's $99)
    expect(wrapper.get('[data-test="sub-template-badge"]').text()).toBe('Claude Starter')
    expect(wrapper.text()).toContain('$15.00')
    expect(wrapper.text()).not.toContain('$99.00')

    // Click Edit Limits button -> opens SubscriptionLimitsEditDialog and submits new limits
    await wrapper.get('[data-test="edit-limits-101"]').trigger('click')
    await wrapper.get('[data-testid="sub-limits-daily-limit"]').setValue('25')
    await wrapper.get('#edit-subscription-limits-form').trigger('submit')
    await flushPromises()
    expect(updateLimitsMock).toHaveBeenCalledWith(
      101,
      expect.objectContaining({ daily_limit_usd: 25 })
    )

    // Switch to Templates tab
    await wrapper.get('[data-test="tab-templates"]').trigger('click')
    expect(wrapper.find('[data-test="templates-tab-panel"]').exists()).toBe(true)
  })
})
