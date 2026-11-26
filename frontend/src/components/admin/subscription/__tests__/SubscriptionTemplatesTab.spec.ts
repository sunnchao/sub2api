import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import SubscriptionTemplatesTab from '../SubscriptionTemplatesTab.vue'
import type { Group } from '@/types'

const { listMock, createMock, updateMock, removeMock, showSuccessMock, showErrorMock } = vi.hoisted(() => ({
  listMock: vi.fn(),
  createMock: vi.fn(),
  updateMock: vi.fn(),
  removeMock: vi.fn(),
  showSuccessMock: vi.fn(),
  showErrorMock: vi.fn()
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (k: string) => k })
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showSuccess: showSuccessMock,
    showError: showErrorMock
  })
}))

vi.mock('@/api/admin/subscriptionTemplates', () => ({
  subscriptionTemplatesAPI: {
    list: listMock,
    create: createMock,
    update: updateMock,
    remove: removeMock
  }
}))

const DataTableStub = defineComponent({
  name: 'DataTable',
  props: { data: { type: Array, default: () => [] }, columns: { type: Array, default: () => [] } },
  template: `
    <div>
      <div v-for="row in data" :key="row.id" :data-row-id="row.id">
        <slot name="cell-name" :row="row" :value="row.name" />
        <slot name="cell-group" :row="row" :value="row.group" />
        <slot name="cell-limits" :row="row" />
        <slot name="cell-actions" :row="row" />
      </div>
    </div>
  `
})

const SelectStub = defineComponent({
  name: 'SelectStub',
  props: { modelValue: [String, Number, null], options: { type: Array, default: () => [] } },
  emits: ['update:modelValue', 'change'],
  template: `
    <select :value="modelValue ?? ''" @change="$emit('update:modelValue', $event.target.value === '' ? null : Number($event.target.value)); $emit('change')">
      <option v-for="o in options" :key="String(o.value)" :value="o.value">{{ o.label }}</option>
    </select>
  `
})

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: { show: Boolean },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>'
})

const ConfirmDialogStub = defineComponent({
  name: 'ConfirmDialog',
  props: { show: Boolean },
  emits: ['confirm', 'cancel'],
  template: '<div v-if="show"><button data-testid="confirm-delete-btn" @click="$emit(\'confirm\')">confirm</button></div>'
})

const groups: Group[] = [
  {
    id: 10,
    name: 'Claude Standard',
    description: null,
    platform: 'anthropic',
    rate_multiplier: 1,
    is_exclusive: false,
    status: 'active',
    subscription_type: 'standard',
    daily_limit_usd: null,
    weekly_limit_usd: null,
    monthly_limit_usd: null,
    created_at: '',
    updated_at: ''
  }
]

describe('SubscriptionTemplatesTab', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    listMock.mockResolvedValue([
      {
        id: 1,
        name: 'Claude Starter',
        description: 'Lite tier',
        group_id: 10,
        daily_limit_usd: 10,
        weekly_limit_usd: null,
        monthly_limit_usd: 100,
        hourly_limit_usd: null,
        hourly_window_hours: 5,
        default_validity_days: 30,
        status: 'active',
        sort_order: 0,
        created_at: '',
        updated_at: '',
        group: groups[0]
      }
    ])
    createMock.mockResolvedValue({ id: 2, name: 'Claude Pro' })
    removeMock.mockResolvedValue({ message: 'ok' })
  })

  it('loads templates, creates a new template bound to standard group, and deletes template', async () => {
    const wrapper = mount(SubscriptionTemplatesTab, {
      props: { groups },
      global: {
        stubs: {
          DataTable: DataTableStub,
          Select: SelectStub,
          BaseDialog: BaseDialogStub,
          ConfirmDialog: ConfirmDialogStub,
          GroupBadge: { props: ['name'], template: '<span>{{ name }}</span>' },
          GroupOptionItem: true,
          Icon: true
        }
      }
    })
    await flushPromises()

    expect(listMock).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('Claude Starter')
    expect(wrapper.text()).toContain('Claude Standard')
    expect(wrapper.text()).toContain('$10')
    expect(wrapper.text()).toContain('$100')

    // Create new template
    await wrapper.get('[data-testid="create-template-btn"]').trigger('click')
    await wrapper.get('[data-testid="template-name-input"]').setValue('Claude Pro')
    await wrapper.get('[data-testid="template-group-select"]').setValue('10')
    await wrapper.get('[data-testid="template-daily-limit"]').setValue('50')
    await wrapper.get('#subscription-template-form').trigger('submit')
    await flushPromises()

    expect(createMock).toHaveBeenCalledWith(
      expect.objectContaining({
        name: 'Claude Pro',
        group_id: 10,
        daily_limit_usd: 50,
        weekly_limit_usd: null
      })
    )

    // Delete existing template
    await wrapper.get('[data-testid="delete-template-1"]').trigger('click')
    await wrapper.get('[data-testid="confirm-delete-btn"]').trigger('click')
    await flushPromises()
    expect(removeMock).toHaveBeenCalledWith(1)
  })
})
