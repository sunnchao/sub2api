import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { AdminGroup } from '@/types'
import GroupsView from '@/views/admin/GroupsView.vue'

const {
  listGroups,
  createGroup,
  updateGroup,
  getModelAllowlistCandidates,
  getUsageSummary,
  getCapacitySummary,
  getLiveCapability,
  showSuccess,
  showError
} = vi.hoisted(() => ({
  listGroups: vi.fn(),
  createGroup: vi.fn(),
  updateGroup: vi.fn(),
  getModelAllowlistCandidates: vi.fn(),
  getUsageSummary: vi.fn(),
  getCapacitySummary: vi.fn(),
  getLiveCapability: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    groups: {
      list: listGroups,
      duplicate: vi.fn(),
      getModelAllowlistCandidates,
      getUsageSummary,
      getCapacitySummary,
      getLiveCapability,
      getAll: vi.fn(),
      create: createGroup,
      update: updateGroup,
      delete: vi.fn(),
      updateSortOrder: vi.fn()
    },
    accounts: {
      list: vi.fn(),
      getById: vi.fn()
    }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showSuccess, showError })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ isSimpleMode: false })
}))

vi.mock('@/stores/onboarding', () => ({
  useOnboardingStore: () => ({
    isCurrentStep: vi.fn(() => false),
    nextStep: vi.fn()
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

function makeGroup(overrides: Partial<AdminGroup> = {}): AdminGroup {
  return {
    id: 101,
    name: 'Token Plan Pro',
    description: null,
    platform: 'composite',
    rate_multiplier: 1,
    rpm_limit: 0,
    is_exclusive: true,
    show_in_model_plaza: false,
    status: 'active',
    subscription_type: 'subscription',
    daily_limit_usd: null,
    weekly_limit_usd: null,
    monthly_limit_usd: 288,
    hourly_limit_usd: 18,
    hourly_window_hours: 5,
    long_context_pricing_enabled: true,
    allow_image_generation: false,
    allow_batch_image_generation: false,
    image_count_billing: false,
    image_rate_independent: false,
    image_rate_multiplier: 1,
    batch_image_discount_multiplier: 0.5,
    batch_image_hold_multiplier: 0.6,
    image_price_1k: null,
    image_price_2k: null,
    image_price_4k: null,
    video_rate_independent: false,
    video_rate_multiplier: 1,
    video_price_480p: null,
    video_price_720p: null,
    video_price_1080p: null,
    web_search_price_per_call: null,
    peak_rate_enabled: false,
    peak_start: '',
    peak_end: '',
    peak_rate_multiplier: 1,
    claude_code_only: false,
    fallback_group_id: null,
    fallback_group_id_on_invalid_request: null,
    allow_messages_dispatch: false,
    default_mapped_model: '',
    messages_dispatch_model_config: undefined,
    require_oauth_only: false,
    require_privacy_set: false,
    created_at: '2026-11-28T00:00:00Z',
    updated_at: '2026-11-28T00:00:00Z',
    model_routing: null,
    model_routing_enabled: false,
    mcp_xml_inject: true,
    supported_model_scopes: [],
    account_count: 1,
    active_account_count: 1,
    rate_limited_account_count: 0,
    model_allowlist: undefined,
    sort_order: 1,
    ...overrides
  }
}

const AppLayoutStub = defineComponent({
  template: '<main><slot /></main>'
})

const TablePageLayoutStub = defineComponent({
  template: '<section><slot name="filters" /><slot name="table" /><slot name="pagination" /></section>'
})

const DataTableStub = defineComponent({
  props: {
    data: { type: Array, default: () => [] },
    columns: { type: Array, default: () => [] },
    loading: { type: Boolean, default: false }
  },
  template: `
    <div>
      <div v-for="row in data" :key="row.id" :data-row-id="row.id">
        <slot name="cell-is_exclusive" :value="row.is_exclusive" :row="row" />
        <slot name="cell-actions" :row="row" />
      </div>
    </div>
  `
})

const BaseDialogStub = defineComponent({
  props: {
    show: { type: Boolean, default: false }
  },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>'
})

function mountView() {
  return mount(GroupsView, {
    global: {
      stubs: {
        AppLayout: AppLayoutStub,
        TablePageLayout: TablePageLayoutStub,
        DataTable: DataTableStub,
        Pagination: true,
        BaseDialog: BaseDialogStub,
        ConfirmDialog: true,
        EmptyState: true,
        Select: true,
        PlatformIcon: true,
        Icon: true,
        GroupCapacityBadge: true,
        GroupRateMultipliersModal: true,
        GroupRPMOverridesModal: true,
        VueDraggable: true
      }
    }
  })
}

describe('GroupsView show_in_model_plaza toggle', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.spyOn(console, 'error').mockImplementation(() => {})
    for (const fn of [
      listGroups,
      createGroup,
      updateGroup,
      getModelAllowlistCandidates,
      getUsageSummary,
      getCapacitySummary,
      getLiveCapability,
      showSuccess,
      showError
    ]) {
      fn.mockReset()
    }
    getModelAllowlistCandidates.mockResolvedValue([])
    getUsageSummary.mockResolvedValue([])
    getCapacitySummary.mockResolvedValue([])
    getLiveCapability.mockResolvedValue({ supported: false })
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('renders plaza badge only for exclusive groups with show_in_model_plaza enabled', async () => {
    listGroups.mockResolvedValue({
      items: [
        makeGroup({ id: 1, name: 'Shown Sub', is_exclusive: true, show_in_model_plaza: true }),
        makeGroup({ id: 2, name: 'Hidden Sub', is_exclusive: true, show_in_model_plaza: false }),
        makeGroup({ id: 3, name: 'Public Std', is_exclusive: false, show_in_model_plaza: true, subscription_type: 'standard' })
      ],
      total: 3,
      page: 1,
      page_size: 20,
      pages: 1
    })

    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-row-id="1"]').find('[data-testid="group-plaza-visible-badge"]').exists()).toBe(true)
    expect(wrapper.get('[data-row-id="2"]').find('[data-testid="group-plaza-visible-badge"]').exists()).toBe(false)
    expect(wrapper.get('[data-row-id="3"]').find('[data-testid="group-plaza-visible-badge"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('allows toggling show_in_model_plaza on an exclusive subscription group and submits it on save', async () => {
    const group = makeGroup({ id: 101, is_exclusive: true, show_in_model_plaza: false, subscription_type: 'subscription' })
    listGroups.mockResolvedValue({
      items: [group],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1
    })
    updateGroup.mockResolvedValue({ ...group, show_in_model_plaza: true })

    const wrapper = mountView()
    await flushPromises()

    const editButton = wrapper.findAll('button').find((btn) => btn.text() === 'common.edit')
    expect(editButton).toBeTruthy()
    await editButton!.trigger('click')
    await flushPromises()

    const toggle = wrapper.get('[data-testid="edit-show-in-model-plaza-toggle"]')
    expect(toggle.attributes('aria-checked')).toBe('false')

    await toggle.trigger('click')
    await flushPromises()
    expect(toggle.attributes('aria-checked')).toBe('true')

    await wrapper.get('#edit-group-form').trigger('submit')
    await flushPromises()

    expect(updateGroup).toHaveBeenCalledTimes(1)
    expect(updateGroup).toHaveBeenCalledWith(
      101,
      expect.objectContaining({
        is_exclusive: true,
        show_in_model_plaza: true
      })
    )
    wrapper.unmount()
  })

  it('hides the model plaza section for public standard groups in edit modal', async () => {
    const group = makeGroup({
      id: 102,
      name: 'Public Standard',
      platform: 'anthropic',
      is_exclusive: false,
      show_in_model_plaza: false,
      subscription_type: 'standard'
    })
    listGroups.mockResolvedValue({
      items: [group],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1
    })

    const wrapper = mountView()
    await flushPromises()

    const editButton = wrapper.findAll('button').find((btn) => btn.text() === 'common.edit')
    await editButton!.trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-testid="edit-show-in-model-plaza-section"]').exists()).toBe(false)
    wrapper.unmount()
  })
})
