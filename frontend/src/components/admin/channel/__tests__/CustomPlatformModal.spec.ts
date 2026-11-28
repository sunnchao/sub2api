import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { CustomPlatformAdmin } from '@/api/admin/customPlatforms'

const { createMock, updateMock, providersMock } = vi.hoisted(() => ({
  createMock: vi.fn(),
  updateMock: vi.fn(),
  providersMock: vi.fn()
}))

vi.mock('@/api/admin/customPlatforms', () => ({
  create: createMock,
  update: updateMock,
  listLitellmProviders: providersMock
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

import CustomPlatformModal from '@/components/admin/channel/CustomPlatformModal.vue'

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: { show: { type: Boolean, default: false }, title: String },
  template: '<div v-if="show" data-testid="base-dialog"><slot /><slot name="footer" /></div>'
})

function mountModal(props: Record<string, unknown> = {}) {
  return mount(CustomPlatformModal, {
    props: { show: true, ...props },
    global: {
      stubs: { BaseDialog: BaseDialogStub }
    }
  })
}

async function fillMinimumValidForm(wrapper: ReturnType<typeof mountModal>) {
  await wrapper.get('[data-testid="custom-platform-id"]').setValue('siliconflow')
  await wrapper.get('[data-testid="custom-platform-display-name"]').setValue('SiliconFlow')
  await wrapper
    .get('[data-testid="base-url-chat_completions"]')
    .setValue('https://api.siliconflow.cn/v1')
}

async function submit(wrapper: ReturnType<typeof mountModal>) {
  await wrapper.get('#custom-platform-form').trigger('submit.prevent')
  await flushPromises()
}

const existingPlatform: CustomPlatformAdmin = {
  id: 'siliconflow',
  display_name: 'SiliconFlow',
  enabled: true,
  sort_order: 5,
  icon: { type: 'builtin', value: 'cloud' },
  color_preset: 'violet',
  litellm_provider: 'siliconflow',
  default_test_model: 'Qwen/Qwen2.5-7B-Instruct',
  supports_embeddings: true,
  supports_images: false,
  multi_protocol: {
    default_mode: 'default',
    routing: 'by_model',
    responses_path: '/responses',
    model_catalog: true,
    modes: [
      {
        mode: 'default',
        base_urls: {
          chat_completions: 'https://api.siliconflow.cn/v1',
          anthropic: 'https://api.siliconflow.cn/anthropic',
          embeddings: 'https://api.siliconflow.cn/v1'
        },
        protocol_rules: [{ pattern: 'claude-*', protocol: 'anthropic' }]
      }
    ]
  },
  usage_counts: { accounts: 1, groups: 0, composite_routes: 0, user_quotas: 0 }
}

describe('CustomPlatformModal', () => {
  beforeEach(() => {
    createMock.mockReset().mockResolvedValue({ ...existingPlatform, id: 'siliconflow' })
    updateMock.mockReset().mockResolvedValue(existingPlatform)
    providersMock.mockReset().mockResolvedValue(['siliconflow', 'anthropic'])
  })

  it('submits the exact create payload contract', async () => {
    const wrapper = mountModal()
    await fillMinimumValidForm(wrapper)
    await wrapper.get('[data-testid="litellm-provider"]').setValue('siliconflow')
    await submit(wrapper)

    expect(createMock).toHaveBeenCalledTimes(1)
    expect(createMock).toHaveBeenCalledWith({
      id: 'siliconflow',
      display_name: 'SiliconFlow',
      enabled: true,
      sort_order: 0,
      icon: { type: 'letter', value: '' },
      color_preset: 'blue',
      litellm_provider: 'siliconflow',
      default_test_model: '',
      supports_embeddings: false,
      supports_images: false,
      multi_protocol: {
        default_mode: 'default',
        routing: 'by_inbound',
        model_catalog: false,
        modes: [
          {
            mode: 'default',
            base_urls: { chat_completions: 'https://api.siliconflow.cn/v1' }
          }
        ]
      }
    })
    expect(wrapper.emitted('saved')).toHaveLength(1)
    expect(wrapper.emitted('close')).toHaveLength(1)
    wrapper.unmount()
  })

  it('blocks submission when required fields and a dialogue base URL are missing', async () => {
    const wrapper = mountModal()
    await submit(wrapper)

    expect(createMock).not.toHaveBeenCalled()
    const summary = wrapper.get('[data-testid="custom-platform-errors"]')
    expect(summary.text()).toContain('admin.customPlatforms.errors.idRequired')
    expect(summary.text()).toContain('admin.customPlatforms.errors.displayNameRequired')
    expect(summary.text()).toContain('admin.customPlatforms.errors.baseUrlRequired')
    expect(wrapper.find('[data-testid="custom-platform-error-id"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('validates the id pattern and base URL scheme', async () => {
    const wrapper = mountModal()
    await wrapper.get('[data-testid="custom-platform-id"]').setValue('Silicon Flow')
    await wrapper.get('[data-testid="custom-platform-display-name"]').setValue('SiliconFlow')
    await wrapper.get('[data-testid="base-url-chat_completions"]').setValue('ftp://example.com')
    await submit(wrapper)

    expect(createMock).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="custom-platform-errors"]').text()).toContain(
      'admin.customPlatforms.errors.idPattern'
    )
    expect(
      wrapper.get('[data-testid="custom-platform-error-base-url-chat_completions"]').text()
    ).toBe('admin.customPlatforms.errors.baseUrlInvalid')
    wrapper.unmount()
  })

  it('rejects unsafe icon URLs but accepts https and site paths', async () => {
    const wrapper = mountModal()
    await fillMinimumValidForm(wrapper)
    await wrapper.get('[data-testid="icon-tab-url"]').trigger('click')
    const urlInput = wrapper.get('[data-testid="custom-platform-icon-value"]')

    await urlInput.setValue('javascript:alert(1)')
    await submit(wrapper)
    expect(wrapper.get('[data-testid="custom-platform-error-icon"]').text()).toBe(
      'admin.customPlatforms.errors.iconUrlInvalid'
    )
    expect(createMock).not.toHaveBeenCalled()

    await urlInput.setValue('https://cdn.example.com/logo.svg')
    expect(wrapper.find('[data-testid="custom-platform-error-icon"]').exists()).toBe(false)

    await urlInput.setValue('/assets/logo.svg')
    expect(wrapper.find('[data-testid="custom-platform-error-icon"]').exists()).toBe(false)

    await submit(wrapper)
    expect(createMock).toHaveBeenCalledTimes(1)
    expect(createMock.mock.calls[0][0].icon).toEqual({
      type: 'url',
      value: '/assets/logo.svg'
    })
    wrapper.unmount()
  })

  it('rejects oversized icon uploads', async () => {
    const wrapper = mountModal()
    await wrapper.get('[data-testid="icon-tab-data_url"]').trigger('click')
    const input = wrapper.get('[data-testid="custom-platform-icon-file"]')
    const file = new File([new Uint8Array(70 * 1024)], 'logo.png', { type: 'image/png' })

    Object.defineProperty(input.element, 'files', { value: [file], configurable: true })
    await input.trigger('change')

    expect(wrapper.get('[data-testid="custom-platform-error-icon"]').text()).toBe(
      'admin.customPlatforms.errors.fileTooLarge'
    )
    wrapper.unmount()
  })

  it('turns an uploaded image into a data URL icon', async () => {
    const wrapper = mountModal()
    await fillMinimumValidForm(wrapper)
    await wrapper.get('[data-testid="icon-tab-data_url"]').trigger('click')
    const input = wrapper.get('[data-testid="custom-platform-icon-file"]')
    // jsdom FileReader 只接受同 realm 的 Blob，使用 window.File 而不是 Node 全局 File。
    const file = new window.File([new Uint8Array([137, 80, 78, 71])], 'logo.png', {
      type: 'image/png'
    })

    Object.defineProperty(input.element, 'files', { value: [file], configurable: true })
    await input.trigger('change')
    // FileReader 是异步的：等 data URL 写入表单后再提交。
    await vi.waitFor(() => {
      expect((wrapper.vm as any).$.setupState.form.icon_value).toMatch(/^data:image\/png;base64,/)
    })
    await submit(wrapper)

    expect(createMock).toHaveBeenCalledTimes(1)
    const icon = createMock.mock.calls[0][0].icon
    expect(icon.type).toBe('data_url')
    expect(icon.value).toMatch(/^data:image\/png;base64,/)
    wrapper.unmount()
  })

  it('encodes extra modes, protocol rules and the model catalog toggle', async () => {
    const wrapper = mountModal()
    await fillMinimumValidForm(wrapper)
    await wrapper.get('[data-testid="protocol-anthropic"]').setValue(true)
    await wrapper.get('[data-testid="base-url-anthropic"]').setValue('https://api.siliconflow.cn/anthropic')
    await wrapper.get('[data-testid="advanced-toggle"]').trigger('click')
    await wrapper.get('[data-testid="add-mode"]').trigger('click')
    await wrapper.get('[data-testid="extra-mode-key-0"]').setValue('cn')
    await wrapper.get('[data-testid="extra-mode-label-0"]').setValue('中国大陆')
    await wrapper
      .get('[data-testid="extra-mode-base-url-0-chat_completions"]')
      .setValue('https://cn.siliconflow.cn/v1')
    await wrapper.get('[data-testid="add-rule"]').trigger('click')
    await wrapper.get('[data-testid="rule-pattern-0"]').setValue('claude-*')
    await wrapper.get('[data-testid="rule-protocol-0"]').setValue('anthropic')
    await wrapper.get('[data-testid="model-catalog"]').trigger('click')
    await submit(wrapper)

    expect(createMock).toHaveBeenCalledTimes(1)
    const payload = createMock.mock.calls[0][0]
    expect(payload.supports_embeddings).toBe(false)
    expect(payload.multi_protocol.model_catalog).toBe(true)
    expect(payload.multi_protocol.modes).toEqual([
      {
        mode: 'default',
        base_urls: {
          chat_completions: 'https://api.siliconflow.cn/v1',
          anthropic: 'https://api.siliconflow.cn/anthropic'
        },
        protocol_rules: [{ pattern: 'claude-*', protocol: 'anthropic' }]
      },
      {
        mode: 'cn',
        label: '中国大陆',
        base_urls: { chat_completions: 'https://cn.siliconflow.cn/v1' }
      }
    ])
    wrapper.unmount()
  })

  it('rejects protocol rules that target a disabled protocol', async () => {
    const wrapper = mountModal()
    await fillMinimumValidForm(wrapper)
    await wrapper.get('[data-testid="protocol-anthropic"]').setValue(true)
    await wrapper.get('[data-testid="base-url-anthropic"]').setValue('https://api.siliconflow.cn/anthropic')
    await wrapper.get('[data-testid="advanced-toggle"]').trigger('click')
    await wrapper.get('[data-testid="add-rule"]').trigger('click')
    await wrapper.get('[data-testid="rule-pattern-0"]').setValue('claude-*')
    await wrapper.get('[data-testid="rule-protocol-0"]').setValue('anthropic')
    // 取消勾选 anthropic 后，仍存在的分流规则指向了未启用的协议。
    await wrapper.get('[data-testid="protocol-anthropic"]').setValue(false)
    await submit(wrapper)

    expect(
      wrapper.get('[data-testid="custom-platform-error-rule-protocol-0"]').text()
    ).toBe('admin.customPlatforms.errors.ruleProtocolInvalid')
    expect(createMock).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('loads an existing platform for editing with an immutable id', async () => {
    const wrapper = mountModal({ platform: existingPlatform, customIds: ['siliconflow'] })

    const idInput = wrapper.get<HTMLInputElement>('[data-testid="custom-platform-id"]')
    expect(idInput.element.value).toBe('siliconflow')
    expect(idInput.element.disabled).toBe(true)
    expect(
      wrapper.get<HTMLInputElement>('[data-testid="base-url-chat_completions"]').element.value
    ).toBe('https://api.siliconflow.cn/v1')

    await submit(wrapper)

    expect(createMock).not.toHaveBeenCalled()
    expect(updateMock).toHaveBeenCalledTimes(1)
    expect(updateMock.mock.calls[0][0]).toBe('siliconflow')
    expect(updateMock.mock.calls[0][1]).toMatchObject({
      id: 'siliconflow',
      display_name: 'SiliconFlow',
      icon: { type: 'builtin', value: 'cloud' },
      color_preset: 'violet',
      supports_embeddings: true,
      multi_protocol: {
        routing: 'by_model',
        responses_path: '/responses',
        model_catalog: true
      }
    })
    wrapper.unmount()
  })

  it('rejects a responses path that is not a clean absolute path', async () => {
    const wrapper = mountModal()
    await fillMinimumValidForm(wrapper)
    await wrapper.get('[data-testid="responses-path"]').setValue('responses?x=1')
    await submit(wrapper)

    expect(wrapper.get('[data-testid="custom-platform-error-responses-path"]').text()).toBe(
      'admin.customPlatforms.errors.responsesPathInvalid'
    )
    expect(createMock).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('rejects duplicate custom platform IDs during creation', async () => {
    const wrapper = mountModal({ customIds: ['siliconflow'] })
    await fillMinimumValidForm(wrapper)
    await submit(wrapper)

    expect(createMock).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="custom-platform-error-id"]').text()).toBe(
      'admin.customPlatforms.errors.idDuplicate'
    )
    wrapper.unmount()
  })

  it('rejects empty or malformed icon URLs and never renders unsafe preview images', async () => {
    const wrapper = mountModal()
    await fillMinimumValidForm(wrapper)
    await wrapper.get('[data-testid="icon-tab-url"]').trigger('click')
    const urlInput = wrapper.get('[data-testid="custom-platform-icon-value"]')

    await urlInput.setValue('')
    await submit(wrapper)
    expect(createMock).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="custom-platform-error-icon"]').text()).toBe(
      'admin.customPlatforms.errors.iconUrlInvalid'
    )

    await urlInput.setValue('javascript:alert(1)')
    expect(wrapper.find('[data-testid="custom-platform-preview"] img').exists()).toBe(false)

    await urlInput.setValue('https://')
    await submit(wrapper)
    expect(createMock).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="custom-platform-preview"] img').exists()).toBe(false)
    wrapper.unmount()
  })

  it('rejects SVG data URLs with slash-separated event handlers or embedded iframes', async () => {
    const wrapper = mountModal()
    await fillMinimumValidForm(wrapper)
    await wrapper.get('[data-testid="icon-tab-data_url"]').trigger('click')
    const input = wrapper.get('[data-testid="custom-platform-icon-file"]')

    const evilSvg = new window.File(['<svg/onload=alert(1)></svg>'], 'evil.svg', {
      type: 'image/svg+xml'
    })
    Object.defineProperty(input.element, 'files', { value: [evilSvg], configurable: true })
    await input.trigger('change')
    await vi.waitFor(() => {
      expect(wrapper.find('[data-testid="custom-platform-error-icon"]').exists()).toBe(true)
    })
    expect(wrapper.get('[data-testid="custom-platform-error-icon"]').text()).toBe(
      'admin.customPlatforms.errors.iconDataInvalid'
    )
    wrapper.unmount()
  })

  it('requires every extra mode to configure at least one dialogue protocol base URL', async () => {
    const wrapper = mountModal()
    await fillMinimumValidForm(wrapper)
    await wrapper.get('[data-testid="advanced-toggle"]').trigger('click')
    await wrapper.get('[data-testid="add-mode"]').trigger('click')
    await wrapper.get('[data-testid="extra-mode-key-0"]').setValue('cn')
    await submit(wrapper)

    expect(createMock).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="custom-platform-error-extra-mode-urls-0"]').text()).toBe(
      'admin.customPlatforms.errors.extraModeNoDialogueProtocol'
    )
    wrapper.unmount()
  })

  it('preserves non-default default_mode, mode label, and extra-mode protocol_rules on edit roundtrip', async () => {
    const multiModePlatform: CustomPlatformAdmin = {
      ...existingPlatform,
      multi_protocol: {
        default_mode: 'cn',
        routing: 'by_model',
        modes: [
          {
            mode: 'cn',
            label: '中国大陆',
            base_urls: { chat_completions: 'https://cn.example.com/v1' },
            protocol_rules: [{ pattern: 'deepseek-*', protocol: 'chat_completions' }]
          },
          {
            mode: 'intl',
            label: '国际区',
            base_urls: {
              chat_completions: 'https://intl.example.com/v1',
              anthropic: 'https://intl.example.com/anthropic'
            },
            protocol_rules: [{ pattern: 'claude-*', protocol: 'anthropic' }]
          }
        ]
      }
    }
    const wrapper = mountModal({ platform: multiModePlatform, customIds: ['siliconflow'] })
    await submit(wrapper)

    expect(updateMock).toHaveBeenCalledTimes(1)
    expect(updateMock.mock.calls[0][1].multi_protocol).toMatchObject({
      default_mode: 'cn',
      modes: [
        {
          mode: 'cn',
          label: '中国大陆',
          base_urls: { chat_completions: 'https://cn.example.com/v1' },
          protocol_rules: [{ pattern: 'deepseek-*', protocol: 'chat_completions' }]
        },
        {
          mode: 'intl',
          label: '国际区',
          base_urls: {
            chat_completions: 'https://intl.example.com/v1',
            anthropic: 'https://intl.example.com/anthropic'
          },
          protocol_rules: [{ pattern: 'claude-*', protocol: 'anthropic' }]
        }
      ]
    })
    wrapper.unmount()
  })
})
