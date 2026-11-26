import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import PlanEditDialog from '../PlanEditDialog.vue'
import { adminPaymentAPI } from '@/api/admin/payment'
import type { AdminGroup } from '@/types'
import type { SubscriptionTemplate } from '@/api/admin/subscriptionTemplates'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (k: string) => k })
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn() })
}))

vi.mock('@/api/admin/payment', () => ({
  adminPaymentAPI: {
    createPlan: vi.fn().mockResolvedValue({ data: { id: 1 } }),
    updatePlan: vi.fn().mockResolvedValue({ data: { id: 1 } })
  }
}))

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: { show: Boolean },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>'
})

const SelectStub = defineComponent({
  name: 'SelectStub',
  props: { modelValue: [String, Number, null], options: { type: Array, default: () => [] }, disabled: Boolean },
  emits: ['update:modelValue', 'change'],
  template: `
    <select
      :value="modelValue ?? ''"
      :disabled="disabled"
      @change="$emit('update:modelValue', $event.target.value === '' ? null : Number($event.target.value)); $emit('change')"
    >
      <option v-for="o in options" :key="String(o.value)" :value="o.value ?? ''">{{ o.label }}</option>
    </select>
  `
})

const stdGroup = {
  id: 10,
  name: 'Standard Claude',
  platform: 'anthropic',
  rate_multiplier: 1,
  status: 'active',
  subscription_type: 'standard',
  daily_limit_usd: null,
  weekly_limit_usd: null,
  monthly_limit_usd: null
} as unknown as AdminGroup

const templateFixture: SubscriptionTemplate = {
  id: 7,
  name: 'Claude Pro Template',
  description: '',
  group_id: 10,
  daily_limit_usd: 30,
  weekly_limit_usd: 150,
  monthly_limit_usd: 400,
  hourly_limit_usd: 8,
  hourly_window_hours: 4,
  default_validity_days: 30,
  status: 'active',
  sort_order: 0,
  created_at: '',
  updated_at: '',
  group: stdGroup
}

describe('PlanEditDialog subscription template binding', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('auto-binds standard group from selected template, previews template limits, and sends template_id', async () => {
    const wrapper = mount(PlanEditDialog, {
      props: {
        show: true,
        plan: null,
        groups: [stdGroup],
        templates: [templateFixture]
      },
      global: {
        stubs: { BaseDialog: BaseDialogStub, Select: SelectStub, GroupBadge: true, Icon: true }
      }
    })

    // Select template #7 -> auto-sets group_id = 10 & previews template limits
    await wrapper.get('[data-testid="plan-template-select"]').setValue('7')
    expect(wrapper.text()).toContain('$30')
    expect(wrapper.text()).toContain('$150')
    expect(wrapper.text()).toContain('$400')

    // Fill required fields & save
    const textInputs = wrapper.findAll('input[type="text"]')
    await textInputs[0].setValue('Pro Monthly')
    await wrapper.find('textarea').setValue('Pro plan desc')
    const numberInputs = wrapper.findAll('input[type="number"]')
    await numberInputs[0].setValue('29.9')

    await wrapper.get('#plan-form').trigger('submit')
    await flushPromises()

    expect(adminPaymentAPI.createPlan).toHaveBeenCalledWith(
      expect.objectContaining({
        name: 'Pro Monthly',
        group_id: 10,
        template_id: 7,
        price: 29.9
      })
    )
  })
})
