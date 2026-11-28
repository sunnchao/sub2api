/**
 * 自定义平台表单的纯数据层：协议/图标/配色白名单、表单状态 ↔ API payload
 * 转换与校验。组件只负责绑定与展示，便于复用与单元测试。
 */

import type { PlatformIconType, ProviderRouting } from '@/constants/platformCatalog'
import type {
  CustomPlatformAdmin,
  CustomPlatformMode,
  CustomPlatformPayload,
  CustomPlatformProtocolRule
} from '@/api/admin/customPlatforms'
import {
  PLATFORM_COLOR_PRESETS,
  type PlatformColorPresetName
} from '@/utils/platformColors'
import { BUILTIN_PLATFORM_ICON_KEYS } from '@/constants/platformIcons'

export { BUILTIN_PLATFORM_ICON_KEYS }

export const CUSTOM_PLATFORM_PROTOCOL_KEYS = [
  'chat_completions',
  'responses',
  'anthropic',
  'embeddings',
  'images'
] as const

export type CustomPlatformProtocolKey = (typeof CUSTOM_PLATFORM_PROTOCOL_KEYS)[number]

export const CUSTOM_PLATFORM_DIALOGUE_PROTOCOL_KEYS: CustomPlatformProtocolKey[] = [
  'chat_completions',
  'responses',
  'anthropic'
]

export const CUSTOM_PLATFORM_COLOR_PRESETS = [
  'blue',
  'violet',
  'emerald',
  'teal',
  'amber',
  'orange',
  'rose',
  'pink',
  'indigo',
  'slate'
] as const

export type CustomPlatformColorPreset = (typeof CUSTOM_PLATFORM_COLOR_PRESETS)[number]

export const CUSTOM_PLATFORM_ID_PATTERN = /^[a-z][a-z0-9_-]{1,31}$/
export const CUSTOM_PLATFORM_MODE_PATTERN = /^[a-z0-9][a-z0-9_-]{0,31}$/
export const RESERVED_CUSTOM_PLATFORM_IDS = [
  'composite',
  'all',
  'default',
  'none',
  'other',
  '__other__'
]

export const CUSTOM_PLATFORM_MAX_ICON_BYTES = 64 * 1024
export const CUSTOM_PLATFORM_MAX_ICON_URL_LENGTH = 2048
export const DEFAULT_CUSTOM_PLATFORM_MODE = 'default'

const DATA_URL_PATTERN = /^data:image\/(png|jpeg|webp|svg\+xml);base64,/i
const HTTP_URL_PATTERN = /^https?:\/\/[^\s]+$/i

/**
 * 10 色预设的徽章 / 文字样式与色块颜色统一取自 platformColors 的共享预设，
 * 保证管理端预览与全站徽章完全一致。
 */
export function presetBadgeClass(preset: string | null | undefined): string {
  return presetColors(preset).badge
}

export function presetTextClass(preset: string | null | undefined): string {
  return presetColors(preset).text
}

/** 预设色块（颜色选择器）使用的主色。 */
export function presetAccentColor(preset: string | null | undefined): string {
  return presetColors(preset).accent
}

function presetColors(preset: string | null | undefined) {
  const key = (preset ?? '') as PlatformColorPresetName
  return PLATFORM_COLOR_PRESETS[key] ?? PLATFORM_COLOR_PRESETS.slate
}

export interface CustomPlatformModeForm {
  mode: string
  label: string
  base_urls: Record<CustomPlatformProtocolKey, string>
  protocol_rules?: CustomPlatformRuleForm[]
}

export interface CustomPlatformRuleForm {
  pattern: string
  protocol: string
}

export interface CustomPlatformFormState {
  id: string
  display_name: string
  enabled: boolean
  sort_order: number | string
  icon_type: PlatformIconType
  icon_value: string
  color_preset: string
  default_mode: string
  default_mode_label: string
  routing: ProviderRouting
  responses_path: string
  model_catalog: boolean
  default_test_model: string
  litellm_provider: string
  protocols: Record<CustomPlatformProtocolKey, boolean>
  base_urls: Record<CustomPlatformProtocolKey, string>
  extra_modes: CustomPlatformModeForm[]
  protocol_rules: CustomPlatformRuleForm[]
}

export type CustomPlatformFormErrors = Record<string, string>

function emptyBaseUrls(): Record<CustomPlatformProtocolKey, string> {
  return {
    chat_completions: '',
    responses: '',
    anthropic: '',
    embeddings: '',
    images: ''
  }
}

function emptyProtocols(): Record<CustomPlatformProtocolKey, boolean> {
  return {
    chat_completions: true,
    responses: false,
    anthropic: false,
    embeddings: false,
    images: false
  }
}

export function createEmptyCustomPlatformForm(): CustomPlatformFormState {
  return {
    id: '',
    display_name: '',
    enabled: true,
    sort_order: 0,
    icon_type: 'letter',
    icon_value: '',
    color_preset: 'blue',
    default_mode: DEFAULT_CUSTOM_PLATFORM_MODE,
    default_mode_label: '',
    routing: 'by_inbound',
    responses_path: '',
    model_catalog: false,
    default_test_model: '',
    litellm_provider: '',
    protocols: emptyProtocols(),
    base_urls: emptyBaseUrls(),
    extra_modes: [],
    protocol_rules: []
  }
}

function modeFormFromApi(mode: CustomPlatformMode): CustomPlatformModeForm {
  const baseUrls = emptyBaseUrls()
  for (const key of CUSTOM_PLATFORM_PROTOCOL_KEYS) {
    baseUrls[key] = mode.base_urls?.[key] ?? ''
  }
  return {
    mode: mode.mode,
    label: mode.label ?? '',
    base_urls: baseUrls,
    protocol_rules: (mode.protocol_rules ?? []).map(rule => ({
      pattern: rule.pattern,
      protocol: rule.protocol
    }))
  }
}

/** 把接口实体还原成表单状态：首个模式（默认模式）映射到基础配置区。 */
export function formFromCustomPlatform(platform: CustomPlatformAdmin): CustomPlatformFormState {
  const profile = platform.multi_protocol
  const modes = Array.isArray(profile?.modes) ? profile.modes : []
  const defaultMode =
    modes.find(item => item.mode === profile?.default_mode) ?? modes[0]
  const protocols = emptyProtocols()
  const baseUrls = emptyBaseUrls()

  if (defaultMode) {
    for (const key of CUSTOM_PLATFORM_PROTOCOL_KEYS) {
      const value = defaultMode.base_urls?.[key] ?? ''
      baseUrls[key] = value
      protocols[key] = value.trim() !== ''
    }
  }
  // 能力开关以实体字段为准（空 Base URL 会回退到 chat 基址）。
  if (platform.supports_embeddings) protocols.embeddings = true
  if (platform.supports_images) protocols.images = true
  if (!CUSTOM_PLATFORM_DIALOGUE_PROTOCOL_KEYS.some(key => protocols[key])) {
    protocols.chat_completions = true
  }

  const defaultModeKey = defaultMode?.mode?.trim() || DEFAULT_CUSTOM_PLATFORM_MODE

  return {
    id: platform.id,
    display_name: platform.display_name,
    enabled: platform.enabled !== false,
    sort_order: platform.sort_order ?? 0,
    icon_type: platform.icon?.type ?? 'letter',
    icon_value: platform.icon?.value ?? '',
    color_preset: platform.color_preset || 'blue',
    default_mode: defaultModeKey,
    default_mode_label: defaultMode?.label ?? '',
    routing: profile?.routing === 'by_model' ? 'by_model' : 'by_inbound',
    responses_path: profile?.responses_path ?? '',
    model_catalog: profile?.model_catalog === true,
    default_test_model: platform.default_test_model ?? '',
    litellm_provider: platform.litellm_provider ?? '',
    protocols,
    base_urls: baseUrls,
    extra_modes: modes
      .filter(item => item.mode !== defaultModeKey)
      .map(modeFormFromApi),
    protocol_rules: (defaultMode?.protocol_rules ?? []).map(rule => ({
      pattern: rule.pattern,
      protocol: rule.protocol
    }))
  }
}

function trimmedBaseUrls(
  values: Record<CustomPlatformProtocolKey, string>,
  allowed: (key: CustomPlatformProtocolKey) => boolean
): Record<string, string> {
  const out: Record<string, string> = {}
  for (const key of CUSTOM_PLATFORM_PROTOCOL_KEYS) {
    if (!allowed(key)) continue
    const value = (values[key] ?? '').trim()
    if (value) out[key] = value
  }
  return out
}

function rulesForPayload(rules: CustomPlatformRuleForm[]): CustomPlatformProtocolRule[] {
  return rules
    .map(rule => ({ pattern: rule.pattern.trim(), protocol: rule.protocol }))
    .filter(rule => rule.pattern !== '')
}

/** 表单状态 → API 请求体，严格匹配后端契约字段。 */
export function buildCustomPlatformPayload(form: CustomPlatformFormState): CustomPlatformPayload {
  const defaultModeKey = form.default_mode.trim() || DEFAULT_CUSTOM_PLATFORM_MODE
  const defaultModeLabel = form.default_mode_label.trim()
  const primaryBaseUrls = trimmedBaseUrls(form.base_urls, key => form.protocols[key])
  const primaryRules = rulesForPayload(form.protocol_rules)
  const modes: CustomPlatformMode[] = [
    {
      mode: defaultModeKey,
      ...(defaultModeLabel ? { label: defaultModeLabel } : {}),
      base_urls: primaryBaseUrls,
      ...(primaryRules.length ? { protocol_rules: primaryRules } : {})
    },
    ...form.extra_modes.map(extra => {
      const baseUrls = trimmedBaseUrls(extra.base_urls, () => true)
      const extraRules = rulesForPayload(extra.protocol_rules ?? [])
      return {
        mode: extra.mode.trim(),
        ...(extra.label.trim() ? { label: extra.label.trim() } : {}),
        base_urls: baseUrls,
        ...(extraRules.length ? { protocol_rules: extraRules } : {})
      }
    })
  ]
  const responsesPath = form.responses_path.trim()

  return {
    id: form.id.trim(),
    display_name: form.display_name.trim(),
    enabled: form.enabled,
    sort_order: Number(form.sort_order) || 0,
    icon: {
      type: form.icon_type,
      value: form.icon_value.trim()
    },
    color_preset: form.color_preset,
    litellm_provider: form.litellm_provider.trim(),
    default_test_model: form.default_test_model.trim(),
    supports_embeddings: form.protocols.embeddings,
    supports_images: form.protocols.images,
    multi_protocol: {
      default_mode: defaultModeKey,
      routing: form.routing,
      ...(responsesPath ? { responses_path: responsesPath } : {}),
      model_catalog: form.model_catalog,
      modes
    }
  }
}

export function validateIconValue(iconType: PlatformIconType, value: string): string | null {
  const trimmed = value.trim()
  if (iconType === 'builtin') {
    return (BUILTIN_PLATFORM_ICON_KEYS as readonly string[]).includes(trimmed)
      ? null
      : 'admin.customPlatforms.errors.iconBuiltinInvalid'
  }
  if (iconType === 'url') {
    if (trimmed.length === 0) {
      return 'admin.customPlatforms.errors.iconUrlInvalid'
    }
    if (trimmed.length > CUSTOM_PLATFORM_MAX_ICON_URL_LENGTH) {
      return 'admin.customPlatforms.errors.iconUrlTooLong'
    }
    if (trimmed.toLowerCase().includes('javascript:') || /\s/.test(trimmed)) {
      return 'admin.customPlatforms.errors.iconUrlInvalid'
    }
    if (trimmed.startsWith('/') && !trimmed.startsWith('//')) {
      return null
    }
    try {
      const parsed = new URL(trimmed)
      if (parsed.protocol === 'https:' && parsed.hostname) {
        return null
      }
    } catch {
      // fall through to invalid error
    }
    return 'admin.customPlatforms.errors.iconUrlInvalid'
  }
  if (iconType === 'data_url') {
    return validateImageDataUrl(trimmed)
  }
  if (iconType === 'letter') {
    return [...trimmed].length > 2 ? 'admin.customPlatforms.errors.iconLetterTooLong' : null
  }
  return 'admin.customPlatforms.errors.iconTypeInvalid'
}

/** data URL 图标校验：MIME 白名单 + 解码后 ≤ 64KB + 拒绝脚本载荷。 */
export function validateImageDataUrl(value: string): string | null {
  const match = DATA_URL_PATTERN.exec(value)
  if (!match) return 'admin.customPlatforms.errors.iconDataInvalid'
  const mime = match[1].toLowerCase()
  const payload = value.slice(match[0].length)
  let decoded = ''
  try {
    decoded = atob(payload)
  } catch {
    return 'admin.customPlatforms.errors.iconDataInvalid'
  }
  if (decoded.length > CUSTOM_PLATFORM_MAX_ICON_BYTES) {
    return 'admin.customPlatforms.errors.iconDataTooLarge'
  }
  if (mime === 'svg+xml') {
    const lowered = decoded.toLowerCase()
    if (
      lowered.includes('<script') ||
      lowered.includes('<foreignobject') ||
      lowered.includes('<iframe') ||
      lowered.includes('<object') ||
      lowered.includes('<embed') ||
      /(?:^|[\s/])on[a-z]+\s*=/.test(lowered) ||
      lowered.includes('javascript:')
    ) {
      return 'admin.customPlatforms.errors.iconDataInvalid'
    }
  }
  return null
}

function isHttpUrl(value: string): boolean {
  return HTTP_URL_PATTERN.test(value)
}

export interface ValidateCustomPlatformOptions {
  creating: boolean
  builtinIds?: Iterable<string>
  customIds?: Iterable<string>
}

/**
 * 校验表单，返回 field → i18n key 的错误映射。字段名与 payload 对齐，
 * 便于页面把错误显示在对应输入框下方。
 */
export function validateCustomPlatformForm(
  form: CustomPlatformFormState,
  options: ValidateCustomPlatformOptions
): CustomPlatformFormErrors {
  const errors: CustomPlatformFormErrors = {}
  const id = form.id.trim()

  if (options.creating) {
    if (!id) {
      errors.id = 'admin.customPlatforms.errors.idRequired'
    } else if (!CUSTOM_PLATFORM_ID_PATTERN.test(id)) {
      errors.id = 'admin.customPlatforms.errors.idPattern'
    } else if (RESERVED_CUSTOM_PLATFORM_IDS.includes(id)) {
      errors.id = 'admin.customPlatforms.errors.idReserved'
    } else if (new Set(options.builtinIds ?? []).has(id)) {
      errors.id = 'admin.customPlatforms.errors.idBuiltin'
    } else if (new Set(options.customIds ?? []).has(id)) {
      errors.id = 'admin.customPlatforms.errors.idDuplicate'
    }
  }

  const displayName = form.display_name.trim()
  if (!displayName) {
    errors.display_name = 'admin.customPlatforms.errors.displayNameRequired'
  } else if ([...displayName].length > 64) {
    errors.display_name = 'admin.customPlatforms.errors.displayNameTooLong'
  }

  const enabledDialogue = CUSTOM_PLATFORM_DIALOGUE_PROTOCOL_KEYS.filter(key => form.protocols[key])
  if (enabledDialogue.length === 0) {
    errors.protocols = 'admin.customPlatforms.errors.noDialogueProtocol'
  }
  for (const key of CUSTOM_PLATFORM_PROTOCOL_KEYS) {
    if (!form.protocols[key]) continue
    const value = (form.base_urls[key] ?? '').trim()
    const dialogue = CUSTOM_PLATFORM_DIALOGUE_PROTOCOL_KEYS.includes(key)
    if (!value) {
      // embeddings / images 可回退到 chat 基址，因此允许留空。
      if (dialogue) {
        errors[`base_urls.${key}`] = 'admin.customPlatforms.errors.baseUrlRequired'
      }
      continue
    }
    if (!isHttpUrl(value)) {
      errors[`base_urls.${key}`] = 'admin.customPlatforms.errors.baseUrlInvalid'
    }
  }

  const responsesPath = form.responses_path.trim()
  if (responsesPath) {
    const invalid =
      !responsesPath.startsWith('/') ||
      responsesPath.includes('..') ||
      responsesPath.includes('?') ||
      responsesPath.includes('#') ||
      /\s/.test(responsesPath)
    if (invalid) {
      errors.responses_path = 'admin.customPlatforms.errors.responsesPathInvalid'
    }
  }

  const iconError = validateIconValue(form.icon_type, form.icon_value)
  if (iconError) {
    errors.icon = iconError
  }

  const defaultModeKey = form.default_mode.trim() || DEFAULT_CUSTOM_PLATFORM_MODE
  const modeKeys = new Set<string>([defaultModeKey])
  form.extra_modes.forEach((extra, index) => {
    const mode = extra.mode.trim()
    if (!mode) {
      errors[`extra_modes.${index}.mode`] = 'admin.customPlatforms.errors.modeKeyRequired'
    } else if (!CUSTOM_PLATFORM_MODE_PATTERN.test(mode)) {
      errors[`extra_modes.${index}.mode`] = 'admin.customPlatforms.errors.modeKeyInvalid'
    } else if (modeKeys.has(mode)) {
      errors[`extra_modes.${index}.mode`] = 'admin.customPlatforms.errors.modeKeyDuplicate'
    }
    if (mode) modeKeys.add(mode)
    if ([...extra.label].length > 64) {
      errors[`extra_modes.${index}.label`] = 'admin.customPlatforms.errors.modeLabelTooLong'
    }
    let hasExtraDialogue = false
    for (const key of CUSTOM_PLATFORM_PROTOCOL_KEYS) {
      const value = (extra.base_urls[key] ?? '').trim()
      if (value && CUSTOM_PLATFORM_DIALOGUE_PROTOCOL_KEYS.includes(key)) {
        hasExtraDialogue = true
      }
      if (value && !isHttpUrl(value)) {
        errors[`extra_modes.${index}.base_urls.${key}`] =
          'admin.customPlatforms.errors.baseUrlInvalid'
      }
    }
    if (!hasExtraDialogue) {
      errors[`extra_modes.${index}.base_urls`] =
        'admin.customPlatforms.errors.extraModeNoDialogueProtocol'
    }
  })

  form.protocol_rules.forEach((rule, index) => {
    if (!rule.pattern.trim()) {
      errors[`protocol_rules.${index}.pattern`] =
        'admin.customPlatforms.errors.rulePatternRequired'
    }
    if (!enabledDialogue.includes(rule.protocol as CustomPlatformProtocolKey)) {
      errors[`protocol_rules.${index}.protocol`] =
        'admin.customPlatforms.errors.ruleProtocolInvalid'
    } else if (!form.protocols[rule.protocol as CustomPlatformProtocolKey]) {
      errors[`protocol_rules.${index}.protocol`] =
        'admin.customPlatforms.errors.ruleProtocolInvalid'
    }
  })

  return errors
}

/** 平台列表里展示的协议集合：优先取 profile，内置平台按网关兜底。 */
export function platformProtocolKeys(spec: {
  gateway?: string
  multi_protocol?: { modes?: Array<{ base_urls?: Record<string, string | undefined> }> }
  supports_embeddings?: boolean
  supports_images?: boolean
}): string[] {
  const keys = new Set<string>()
  for (const mode of spec.multi_protocol?.modes ?? []) {
    for (const key of Object.keys(mode.base_urls ?? {})) keys.add(key)
  }
  if (keys.size === 0) {
    if (spec.gateway === 'anthropic' || spec.gateway === 'antigravity') {
      keys.add('anthropic')
    } else if (spec.gateway === 'openai' || spec.gateway === 'gemini') {
      keys.add('chat_completions')
    }
  }
  if (spec.supports_embeddings) keys.add('embeddings')
  if (spec.supports_images) keys.add('images')
  return CUSTOM_PLATFORM_PROTOCOL_KEYS.filter(key => keys.has(key))
}
