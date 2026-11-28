/**
 * Admin Custom Platforms API endpoints
 *
 * 自定义渠道平台是一等平台：ID 创建后不可修改，有账号/分组/路由/限额引用时
 * 不能物理删除（后端返回 409 + usage_counts）。
 */

import { apiClient } from '../client'
import type {
  PlatformIconSpec,
  PlatformIconType,
  PlatformSpec,
  ProviderRouting
} from '@/constants/platformCatalog'

export interface CustomPlatformUsageCounts {
  accounts: number
  groups: number
  composite_routes: number
  user_quotas: number
}

export interface CustomPlatformProtocolRule {
  pattern: string
  protocol: string
}

export interface CustomPlatformMode {
  mode: string
  label?: string
  /** 以上游协议为键的基址；缺省表示该模式不提供该原生端点。 */
  base_urls: Record<string, string>
  protocol_rules?: CustomPlatformProtocolRule[]
}

export interface CustomPlatformProfile {
  default_mode: string
  routing: ProviderRouting
  responses_path?: string
  model_catalog?: boolean
  modes: CustomPlatformMode[]
}

export interface CustomPlatformAdmin {
  id: string
  display_name: string
  enabled: boolean
  sort_order: number
  icon: PlatformIconSpec
  color_preset: string
  litellm_provider: string
  default_test_model: string
  supports_embeddings: boolean
  supports_images: boolean
  multi_protocol: CustomPlatformProfile
  usage_counts?: CustomPlatformUsageCounts
  created_at?: string
  updated_at?: string
}

/** 创建/更新请求体：id 在更新时由后端校验不可变。 */
export type CustomPlatformPayload = Omit<
  CustomPlatformAdmin,
  'usage_counts' | 'created_at' | 'updated_at'
>

export interface CustomPlatformListResponse {
  builtin_platforms: PlatformSpec[]
  custom_platforms: CustomPlatformAdmin[]
}

export interface LiteLLMProvidersResponse {
  providers: string[]
}

export interface CustomPlatformInUseError extends Error {
  status: 409
  reason: 'PLATFORM_IN_USE'
  usage_counts: CustomPlatformUsageCounts
}

export const EMPTY_USAGE_COUNTS: CustomPlatformUsageCounts = {
  accounts: 0,
  groups: 0,
  composite_routes: 0,
  user_quotas: 0
}

export function isCustomPlatformInUseError(error: unknown): error is CustomPlatformInUseError {
  return (
    typeof error === 'object' &&
    error !== null &&
    (error as { status?: unknown }).status === 409 &&
    !!(error as { usage_counts?: unknown }).usage_counts
  )
}

function readUsageCounts(error: unknown): CustomPlatformUsageCounts | null {
  if (typeof error !== 'object' || error === null) return null
  const record = error as Record<string, any>
  const candidates = [
    record.usage_counts,
    record.metadata?.usage_counts,
    record.data?.usage_counts,
    record.response?.data?.data?.usage_counts,
    record.response?.data?.metadata?.usage_counts
  ]
  for (const candidate of candidates) {
    if (typeof candidate === 'string' && candidate.trim()) {
      try {
        const parsed = JSON.parse(candidate)
        if (parsed && typeof parsed === 'object') {
          return normalizeUsageCounts(parsed as Partial<CustomPlatformUsageCounts>)
        }
      } catch {
        // Not JSON: ignore and try the next candidate.
      }
      continue
    }
    if (candidate && typeof candidate === 'object') {
      return normalizeUsageCounts(candidate as Partial<CustomPlatformUsageCounts>)
    }
  }
  return null
}

export function normalizeUsageCounts(
  counts: Partial<CustomPlatformUsageCounts> | null | undefined
): CustomPlatformUsageCounts {
  return {
    accounts: Number(counts?.accounts ?? 0),
    groups: Number(counts?.groups ?? 0),
    composite_routes: Number(counts?.composite_routes ?? 0),
    user_quotas: Number(counts?.user_quotas ?? 0)
  }
}

export async function list(options?: { signal?: AbortSignal }): Promise<CustomPlatformListResponse> {
  const { data } = await apiClient.get<CustomPlatformListResponse>('/admin/custom-platforms', {
    signal: options?.signal
  })
  return {
    builtin_platforms: Array.isArray(data?.builtin_platforms) ? data.builtin_platforms : [],
    custom_platforms: Array.isArray(data?.custom_platforms) ? data.custom_platforms : []
  }
}

export async function create(payload: CustomPlatformPayload): Promise<CustomPlatformAdmin> {
  const { data } = await apiClient.post<CustomPlatformAdmin>('/admin/custom-platforms', payload)
  return data
}

export async function update(id: string, payload: CustomPlatformPayload): Promise<CustomPlatformAdmin> {
  const { data } = await apiClient.put<CustomPlatformAdmin>(
    `/admin/custom-platforms/${encodeURIComponent(id)}`,
    payload
  )
  return data
}

/**
 * 删除自定义平台。
 *
 * 后端在仍有引用时返回 409 PLATFORM_IN_USE；axios 拦截器只保留
 * `{status, reason, message, metadata}`，因此这里把 usage_counts 归一化到
 * 抛出的错误上，供页面展示 409 引用明细（拿不到时页面会回查列表）。
 */
export async function deletePlatform(id: string): Promise<{ deleted: boolean }> {
  try {
    const { data } = await apiClient.delete<{ deleted: boolean }>(
      `/admin/custom-platforms/${encodeURIComponent(id)}`
    )
    return data ?? { deleted: true }
  } catch (error) {
    const usageCounts = readUsageCounts(error)
    if (usageCounts) {
      const message =
        typeof error === 'object' && error !== null && 'message' in error
          ? String((error as { message?: unknown }).message ?? '')
          : ''
      throw Object.assign(new Error(message || 'Custom platform is still in use'), {
        status: 409,
        reason: 'PLATFORM_IN_USE',
        usage_counts: usageCounts,
        cause: error
      }) as CustomPlatformInUseError
    }
    throw error
  }
}

export async function listLitellmProviders(): Promise<string[]> {
  const { data } = await apiClient.get<LiteLLMProvidersResponse>(
    '/admin/custom-platforms/litellm-providers'
  )
  return Array.isArray(data?.providers) ? data.providers : []
}

/** 表单默认值：新平台的图标来源与配色。 */
export const DEFAULT_CUSTOM_PLATFORM_ICON_TYPE: PlatformIconType = 'letter'
export const DEFAULT_CUSTOM_PLATFORM_COLOR_PRESET = 'blue'

const customPlatformsAPI = {
  list,
  create,
  update,
  delete: deletePlatform,
  listLitellmProviders
}

export default customPlatformsAPI
