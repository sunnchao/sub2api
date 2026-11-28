import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { CustomPlatformAdmin } from '@/api/admin/customPlatforms'

const { listMock, updateMock, deleteMock, loadPlatformCatalogMock } = vi.hoisted(() => ({
  listMock: vi.fn(),
  updateMock: vi.fn(),
  deleteMock: vi.fn(),
  loadPlatformCatalogMock: vi.fn()
}))

vi.mock('@/constants/platformCatalogLoader', () => ({
  loadPlatformCatalog: loadPlatformCatalogMock
}))

vi.mock('@/api/admin/customPlatforms', async () => {
  const actual = await vi.importActual<typeof import('@/api/admin/customPlatforms')>(
    '@/api/admin/customPlatforms'
  )
  return {
    ...actual,
    list: listMock,
    update: updateMock,
    deletePlatform: deleteMock
  }
})

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => (params?.name ? `${key}:${params.name}` : key) })
  }
})

import CustomPlatformsView from '@/views/admin/CustomPlatformsView.vue'

const platform: CustomPlatformAdmin = {
  id: 'siliconflow',
  display_name: 'SiliconFlow',
  enabled: true,
  sort_order: 3,
  icon: { type: 'letter', value: 'S' },
  color_preset: 'violet',
  litellm_provider: 'siliconflow',
  default_test_model: '',
  supports_embeddings: true,
  supports_images: false,
  multi_protocol: {
    default_mode: 'default',
    routing: 'by_inbound',
    modes: [
      {
        mode: 'default',
        base_urls: {
          chat_completions: 'https://api.siliconflow.cn/v1',
          embeddings: 'https://api.siliconflow.cn/v1'
        }
      }
    ]
  },
  usage_counts: { accounts: 4, groups: 2, composite_routes: 0, user_quotas: 1 }
}

function mountView() {
  return mount(CustomPlatformsView, {
    global: {
      stubs: {
        AppLayout: defineComponent({ template: '<main><slot /></main>' }),
        BaseDialog: defineComponent({
          props: { show: { type: Boolean, default: false }, title: String },
          template: '<section v-if="show"><slot /><slot name="footer" /></section>'
        }),
        CustomPlatformModal: defineComponent({
          name: 'CustomPlatformModal',
          props: { show: { type: Boolean, default: false } },
          emits: ['close', 'saved'],
          template: '<div v-if="show" data-testid="custom-platform-modal-stub"><button data-testid="emit-saved" @click="$emit(\'saved\', {})" /></div>'
        }),
        PlatformIcon: true,
        Icon: true
      }
    }
  })
}

describe('CustomPlatformsView', () => {
  beforeEach(() => {
    listMock.mockReset().mockResolvedValue({
      builtin_platforms: [
        { id: 'openai', display_name: 'OpenAI', gateway: 'openai', cn_provider: false },
        { id: 'anthropic', display_name: 'Anthropic', gateway: 'anthropic', cn_provider: false }
      ],
      custom_platforms: [platform]
    })
    updateMock.mockReset()
    deleteMock.mockReset()
    loadPlatformCatalogMock.mockReset().mockResolvedValue(null)
  })

  it('renders builtin platforms read-only and the custom platform table', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('[data-testid="builtin-platform-openai"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="builtin-platform-anthropic"]').exists()).toBe(true)
    const row = wrapper.get('[data-testid="custom-platform-row-siliconflow"]')
    expect(row.text()).toContain('SiliconFlow')
    expect(row.text()).toContain('siliconflow')
    expect(row.text()).toContain('admin.customPlatforms.protocols.chat_completions')
    expect(row.text()).toContain('admin.customPlatforms.protocols.embeddings')
    expect(row.text()).toContain('admin.customPlatforms.usage.accounts 4')
    wrapper.unmount()
  })

  it('persists the enabled switch through the update endpoint', async () => {
    updateMock.mockResolvedValue({ ...platform, enabled: false })
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="toggle-platform-siliconflow"]').trigger('click')
    await flushPromises()

    expect(updateMock).toHaveBeenCalledTimes(1)
    expect(updateMock.mock.calls[0][0]).toBe('siliconflow')
    expect(updateMock.mock.calls[0][1]).toMatchObject({ id: 'siliconflow', enabled: false })
    expect(loadPlatformCatalogMock).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('shows the 409 usage breakdown instead of deleting a referenced platform', async () => {
    deleteMock.mockRejectedValue({
      status: 409,
      reason: 'PLATFORM_IN_USE',
      message: 'still referenced',
      usage_counts: { accounts: 4, groups: 2, composite_routes: 0, user_quotas: 1 }
    })
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="delete-platform-siliconflow"]').trigger('click')
    expect(wrapper.text()).toContain('admin.customPlatforms.deleteDialog.message:SiliconFlow')
    await wrapper.get('[data-testid="confirm-delete-platform"]').trigger('click')
    await flushPromises()

    expect(deleteMock).toHaveBeenCalledWith('siliconflow')
    expect(wrapper.find('[data-testid="usage-breakdown"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="usage-accounts"]').text()).toBe('4')
    expect(wrapper.get('[data-testid="usage-groups"]').text()).toBe('2')
    expect(wrapper.get('[data-testid="usage-user_quotas"]').text()).toBe('1')
    expect(wrapper.find('[data-testid="custom-platform-row-siliconflow"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="confirm-delete-platform"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('reloads usage counts when the 409 body is dropped by the interceptor', async () => {
    deleteMock.mockRejectedValue({ status: 409, reason: 'PLATFORM_IN_USE', message: 'still referenced' })
    listMock.mockResolvedValueOnce({
      builtin_platforms: [],
      custom_platforms: [platform]
    })
    listMock.mockResolvedValueOnce({
      builtin_platforms: [],
      custom_platforms: [
        { ...platform, usage_counts: { accounts: 9, groups: 0, composite_routes: 2, user_quotas: 0 } }
      ]
    })
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="delete-platform-siliconflow"]').trigger('click')
    await wrapper.get('[data-testid="confirm-delete-platform"]').trigger('click')
    await flushPromises()

    expect(listMock).toHaveBeenCalledTimes(2)
    expect(wrapper.get('[data-testid="usage-accounts"]').text()).toBe('9')
    expect(wrapper.get('[data-testid="usage-composite_routes"]').text()).toBe('2')
    wrapper.unmount()
  })

  it('refreshes the global platform catalog after save and delete', async () => {
    deleteMock.mockResolvedValue(undefined)
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="add-platform"]').trigger('click')
    await wrapper.get('[data-testid="emit-saved"]').trigger('click')
    await flushPromises()
    expect(loadPlatformCatalogMock).toHaveBeenCalledTimes(1)

    await wrapper.get('[data-testid="delete-platform-siliconflow"]').trigger('click')
    await wrapper.get('[data-testid="confirm-delete-platform"]').trigger('click')
    await flushPromises()
    expect(loadPlatformCatalogMock).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  it('filters both builtin and custom platforms by name or id', async () => {
    const wrapper = mountView()
    await flushPromises()

    const search = wrapper.get('[data-testid="platform-search"]')
    await search.setValue('silicon')
    expect(wrapper.find('[data-testid="custom-platform-row-siliconflow"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="builtin-platform-openai"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="builtin-platforms-no-match"]').exists()).toBe(true)

    await search.setValue('anthropic')
    expect(wrapper.find('[data-testid="builtin-platform-anthropic"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="custom-platform-row-siliconflow"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="custom-platforms-no-match"]').exists()).toBe(true)
    wrapper.unmount()
  })
})
