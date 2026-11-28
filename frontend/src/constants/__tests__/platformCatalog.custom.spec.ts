import { afterEach, describe, expect, it, vi } from 'vitest'

import {
  BUILTIN_PLATFORM_CATALOG,
  getPlatformSpec,
  getProviderMode,
  listEnabledPlatforms,
  listPlatforms,
  resetPlatformCatalog,
  setPlatformCatalog,
  type PlatformCatalog
} from '@/constants/platformCatalog'
import { loadPlatformCatalog } from '@/constants/platformCatalogLoader'

const { fetchPlatformCatalogMock } = vi.hoisted(() => ({
  fetchPlatformCatalogMock: vi.fn()
}))

vi.mock('@/api/platformCatalog', () => ({
  fetchPlatformCatalog: fetchPlatformCatalogMock
}))

const customCatalog: PlatformCatalog = {
  platforms: [
    ...BUILTIN_PLATFORM_CATALOG.platforms,
    {
      id: 'siliconflow',
      display_name: 'SiliconFlow',
      gateway: 'openai',
      cn_provider: false,
      litellm_provider: 'siliconflow',
      custom: true,
      sort_order: 10,
      supports_embeddings: true,
      supports_images: false,
      default_test_model: 'Qwen/Qwen2.5-7B-Instruct',
      icon: { type: 'letter', value: 'S' },
      color_preset: 'violet',
      multi_protocol: {
        default_mode: 'default',
        routing: 'by_inbound',
        modes: [
          {
            mode: 'default',
            label: '默认端点',
            base_urls: {
              chat_completions: 'https://api.siliconflow.cn/v1',
              anthropic: 'https://api.siliconflow.cn/anthropic'
            }
          }
        ]
      }
    },
    {
      id: 'retired_router',
      display_name: 'Retired Router',
      gateway: 'openai',
      cn_provider: false,
      custom: true,
      disabled: true,
      sort_order: 20
    }
  ],
  composite_precedence: [...BUILTIN_PLATFORM_CATALOG.composite_precedence, 'siliconflow']
}

describe('platform catalog with dynamic custom platforms', () => {
  afterEach(() => {
    resetPlatformCatalog()
    fetchPlatformCatalogMock.mockReset()
  })

  it('keeps every platform in listPlatforms but hides disabled ones from new selections', () => {
    setPlatformCatalog(customCatalog)

    expect(listPlatforms().map(spec => spec.id)).toContain('retired_router')

    const enabledIds = listEnabledPlatforms().map(spec => spec.id)
    expect(enabledIds).toContain('siliconflow')
    expect(enabledIds).not.toContain('retired_router')
    expect(enabledIds).toContain('openai')
  })

  it('treats admin-style enabled:false as disabled as well', () => {
    setPlatformCatalog({
      platforms: [{ id: 'admin_only', display_name: 'Admin Only', gateway: 'openai', cn_provider: false, enabled: false }],
      composite_precedence: []
    })

    expect(listEnabledPlatforms()).toEqual([])
  })

  it('exposes custom metadata and mode labels to consumers', () => {
    setPlatformCatalog(customCatalog)

    const spec = getPlatformSpec('siliconflow')
    expect(spec).toMatchObject({
      display_name: 'SiliconFlow',
      custom: true,
      color_preset: 'violet',
      supports_embeddings: true,
      supports_images: false,
      default_test_model: 'Qwen/Qwen2.5-7B-Instruct',
      icon: { type: 'letter', value: 'S' }
    })
    expect(getProviderMode('siliconflow', 'default')?.label).toBe('默认端点')
  })

  it('loads the public catalog and installs it', async () => {
    fetchPlatformCatalogMock.mockResolvedValue(customCatalog)

    await expect(loadPlatformCatalog()).resolves.toBe(customCatalog)
    expect(getPlatformSpec('siliconflow')?.display_name).toBe('SiliconFlow')
  })

  it('silently keeps the builtin fallback when loading fails', async () => {
    fetchPlatformCatalogMock.mockRejectedValue(new Error('offline'))

    await expect(loadPlatformCatalog()).resolves.toBeNull()
    expect(listPlatforms()).toEqual(BUILTIN_PLATFORM_CATALOG.platforms)
  })
})
