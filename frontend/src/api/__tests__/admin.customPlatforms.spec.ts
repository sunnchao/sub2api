import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, post, put, del } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  del: vi.fn()
}))

vi.mock('@/api/client', () => ({
  apiClient: { get, post, put, delete: del }
}))

import {
  create,
  deletePlatform,
  isCustomPlatformInUseError,
  list,
  listLitellmProviders,
  update,
  type CustomPlatformPayload
} from '@/api/admin/customPlatforms'

const payload: CustomPlatformPayload = {
  id: 'siliconflow',
  display_name: 'SiliconFlow',
  enabled: true,
  sort_order: 10,
  icon: { type: 'letter', value: '' },
  color_preset: 'violet',
  litellm_provider: 'siliconflow',
  default_test_model: 'Qwen/Qwen2.5-7B-Instruct',
  supports_embeddings: true,
  supports_images: false,
  multi_protocol: {
    default_mode: 'default',
    routing: 'by_inbound',
    model_catalog: false,
    modes: [
      {
        mode: 'default',
        base_urls: {
          chat_completions: 'https://api.siliconflow.cn/v1',
          anthropic: 'https://api.siliconflow.cn/anthropic'
        }
      }
    ]
  }
}

describe('admin custom platforms API', () => {
  beforeEach(() => {
    get.mockReset()
    post.mockReset()
    put.mockReset()
    del.mockReset()
  })

  it('lists builtin and custom platforms from the unwrapped payload', async () => {
    const customPlatform = { ...payload, usage_counts: { accounts: 2, groups: 1, composite_routes: 0, user_quotas: 3 } }
    get.mockResolvedValue({
      data: {
        builtin_platforms: [{ id: 'openai', display_name: 'OpenAI', gateway: 'openai', cn_provider: false }],
        custom_platforms: [customPlatform]
      }
    })

    const result = await list()

    expect(get).toHaveBeenCalledWith('/admin/custom-platforms', { signal: undefined })
    expect(result.builtin_platforms.map(spec => spec.id)).toEqual(['openai'])
    expect(result.custom_platforms[0].usage_counts).toEqual({
      accounts: 2,
      groups: 1,
      composite_routes: 0,
      user_quotas: 3
    })
  })

  it('creates a platform with the exact contract payload', async () => {
    post.mockResolvedValue({ data: { ...payload, usage_counts: undefined } })

    expect(await create(payload)).toMatchObject({ id: 'siliconflow' })
    expect(post).toHaveBeenCalledWith('/admin/custom-platforms', payload)
  })

  it('updates by id and URL-encodes the identifier', async () => {
    put.mockResolvedValue({ data: { ...payload, enabled: false } })

    await update('silicon flow', { ...payload, enabled: false })

    expect(put).toHaveBeenCalledWith(
      '/admin/custom-platforms/silicon%20flow',
      expect.objectContaining({ enabled: false })
    )
  })

  it('deletes a platform', async () => {
    del.mockResolvedValue({ data: { deleted: true } })

    await expect(deletePlatform('siliconflow')).resolves.toEqual({ deleted: true })
    expect(del).toHaveBeenCalledWith('/admin/custom-platforms/siliconflow')
  })

  it('surfaces the 409 usage breakdown from interceptor metadata', async () => {
    del.mockRejectedValue({
      status: 409,
      reason: 'PLATFORM_IN_USE',
      message: 'custom platform is still referenced',
      metadata: {
        usage_counts: { accounts: 4, groups: 2, composite_routes: 1, user_quotas: 5 }
      }
    })

    const error = await deletePlatform('siliconflow').catch(reason => reason)

    expect(isCustomPlatformInUseError(error)).toBe(true)
    expect(error.usage_counts).toEqual({
      accounts: 4,
      groups: 2,
      composite_routes: 1,
      user_quotas: 5
    })
  })

  it('surfaces the 409 usage breakdown from the raw handler data field', async () => {
    del.mockRejectedValue({
      status: 409,
      reason: 'PLATFORM_IN_USE',
      message: 'custom platform is still referenced',
      response: {
        data: {
          code: 409,
          reason: 'PLATFORM_IN_USE',
          data: { usage_counts: { accounts: 1, groups: 0, composite_routes: 0, user_quotas: 0 } }
        }
      }
    })

    const error = await deletePlatform('siliconflow').catch(reason => reason)

    expect(isCustomPlatformInUseError(error)).toBe(true)
    expect(error.usage_counts.accounts).toBe(1)
  })

  it('rethrows unrelated delete failures untouched', async () => {
    const failure = { status: 500, message: 'boom' }
    del.mockRejectedValue(failure)

    await expect(deletePlatform('siliconflow')).rejects.toBe(failure)
  })

  it('returns the LiteLLM provider list', async () => {
    get.mockResolvedValue({ data: { providers: ['anthropic', 'openai'] } })

    await expect(listLitellmProviders()).resolves.toEqual(['anthropic', 'openai'])
    expect(get).toHaveBeenCalledWith('/admin/custom-platforms/litellm-providers')
  })
})
