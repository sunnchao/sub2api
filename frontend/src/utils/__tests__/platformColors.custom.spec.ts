import { afterEach, describe, expect, it } from 'vitest'
import {
  BUILTIN_PLATFORM_CATALOG,
  resetPlatformCatalog,
  setPlatformCatalog,
  type PlatformCatalog,
  type PlatformSpec
} from '@/constants/platformCatalog'
import {
  PLATFORM_COLOR_PRESETS,
  platformAccentBarClass,
  platformAccentColor,
  platformBadgeClass,
  platformBadgeLightClass,
  platformBorderClass,
  platformBorderStrongClass,
  platformButtonClass,
  platformDiscountClass,
  platformGradientClass,
  platformGradientSubtextClass,
  platformGradientTextClass,
  platformIconClass,
  platformTextClass
} from '@/utils/platformColors'

const PRESET_NAMES = [
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

function catalogWithCustom(spec: Omit<PlatformSpec, 'gateway' | 'cn_provider'> & Partial<PlatformSpec>): PlatformCatalog {
  return {
    platforms: [...BUILTIN_PLATFORM_CATALOG.platforms, { gateway: 'openai', cn_provider: false, ...spec } as PlatformSpec],
    composite_precedence: BUILTIN_PLATFORM_CATALOG.composite_precedence
  }
}

describe('platform color presets', () => {
  afterEach(() => {
    resetPlatformCatalog()
  })

  it('exposes the ten presets with every class slot', () => {
    expect(Object.keys(PLATFORM_COLOR_PRESETS).sort()).toEqual([...PRESET_NAMES].sort())

    for (const name of PRESET_NAMES) {
      const preset = PLATFORM_COLOR_PRESETS[name]
      expect(preset.badge).toMatch(/\bbg-\w+-500\/10\b/)
      expect(preset.badgeLight).toMatch(/\bbg-\w+-500\/10\b/)
      expect(preset.border).toMatch(/\bborder-\w+-500\/20\b/)
      expect(preset.borderStrong).toMatch(/\bborder-\w+-500\/35\b/)
      expect(preset.accent).toMatch(/^#[0-9a-f]{6}$/)
      expect(preset.accentBar).toMatch(/\bfrom-\w+-400\b/)
      expect(preset.text).toMatch(/\btext-\w+-600\b/)
      expect(preset.icon).toMatch(/\btext-\w+-500\b/)
      expect(preset.button).toMatch(/\bbg-\w+-(?:500|600)\b/)
      expect(preset.discount).toMatch(/\bbg-\w+-100\b/)
      expect(preset.gradient).toMatch(/\bfrom-\w+-500\b/)
      expect(preset.gradientText).toMatch(/\btext-\w+-100\b/)
      expect(preset.gradientSubtext).toMatch(/\btext-\w+-200\b/)
    }
  })

  it('resolves a custom platform across all 13 style helpers from its color_preset', () => {
    setPlatformCatalog(
      catalogWithCustom({
        id: 'siliconflow',
        display_name: 'SiliconFlow',
        custom: true,
        color_preset: 'violet'
      })
    )

    expect(platformBadgeClass('siliconflow')).toBe(PLATFORM_COLOR_PRESETS.violet.badge)
    expect(platformBadgeLightClass('siliconflow')).toBe(PLATFORM_COLOR_PRESETS.violet.badgeLight)
    expect(platformBorderClass('siliconflow')).toBe(PLATFORM_COLOR_PRESETS.violet.border)
    expect(platformBorderStrongClass('siliconflow')).toBe(PLATFORM_COLOR_PRESETS.violet.borderStrong)
    expect(platformAccentColor('siliconflow')).toBe(PLATFORM_COLOR_PRESETS.violet.accent)
    expect(platformAccentBarClass('siliconflow')).toBe(PLATFORM_COLOR_PRESETS.violet.accentBar)
    expect(platformTextClass('siliconflow')).toBe(PLATFORM_COLOR_PRESETS.violet.text)
    expect(platformIconClass('siliconflow')).toBe(PLATFORM_COLOR_PRESETS.violet.icon)
    expect(platformButtonClass('siliconflow')).toBe(PLATFORM_COLOR_PRESETS.violet.button)
    expect(platformDiscountClass('siliconflow')).toBe(PLATFORM_COLOR_PRESETS.violet.discount)
    expect(platformGradientClass('siliconflow')).toBe(PLATFORM_COLOR_PRESETS.violet.gradient)
    expect(platformGradientTextClass('siliconflow')).toBe(PLATFORM_COLOR_PRESETS.violet.gradientText)
    expect(platformGradientSubtextClass('siliconflow')).toBe(PLATFORM_COLOR_PRESETS.violet.gradientSubtext)
  })

  it('falls back to slate for unknown platforms and missing or unknown presets', () => {
    setPlatformCatalog({
      platforms: [
        ...BUILTIN_PLATFORM_CATALOG.platforms,
        { id: 'no_preset', display_name: 'No Preset', gateway: 'openai', cn_provider: false, custom: true },
        {
          id: 'bad_preset',
          display_name: 'Bad Preset',
          gateway: 'openai',
          cn_provider: false,
          custom: true,
          color_preset: 'chartreuse'
        }
      ],
      composite_precedence: BUILTIN_PLATFORM_CATALOG.composite_precedence
    })

    expect(platformBadgeClass('mystery')).toBe(PLATFORM_COLOR_PRESETS.slate.badge)
    expect(platformBadgeLightClass('mystery')).toBe(PLATFORM_COLOR_PRESETS.slate.badgeLight)
    expect(platformBorderClass('mystery')).toBe(PLATFORM_COLOR_PRESETS.slate.border)
    expect(platformBorderStrongClass('mystery')).toBe(PLATFORM_COLOR_PRESETS.slate.borderStrong)
    expect(platformAccentColor('mystery')).toBe(PLATFORM_COLOR_PRESETS.slate.accent)
    expect(platformAccentBarClass('mystery')).toBe(PLATFORM_COLOR_PRESETS.slate.accentBar)
    expect(platformTextClass('mystery')).toBe(PLATFORM_COLOR_PRESETS.slate.text)
    expect(platformIconClass('mystery')).toBe(PLATFORM_COLOR_PRESETS.slate.icon)
    expect(platformButtonClass('mystery')).toBe(PLATFORM_COLOR_PRESETS.slate.button)
    expect(platformDiscountClass('mystery')).toBe(PLATFORM_COLOR_PRESETS.slate.discount)
    expect(platformGradientClass('mystery')).toBe(PLATFORM_COLOR_PRESETS.slate.gradient)
    expect(platformGradientTextClass('mystery')).toBe(PLATFORM_COLOR_PRESETS.slate.gradientText)
    expect(platformGradientSubtextClass('mystery')).toBe(PLATFORM_COLOR_PRESETS.slate.gradientSubtext)

    expect(platformBadgeClass('no_preset')).toBe(PLATFORM_COLOR_PRESETS.slate.badge)
    expect(platformBadgeClass('bad_preset')).toBe(PLATFORM_COLOR_PRESETS.slate.badge)
  })

  it('keeps every builtin platform on its existing class strings', () => {
    setPlatformCatalog(
      catalogWithCustom({
        id: 'siliconflow',
        display_name: 'SiliconFlow',
        custom: true,
        color_preset: 'violet'
      })
    )

    expect(platformBadgeClass('gemini')).toBe(
      'bg-blue-500/10 text-blue-600 border-blue-500/30 dark:text-blue-400'
    )
    expect(platformBadgeLightClass('qwen')).toBe(
      'bg-violet-500/10 text-violet-700 dark:bg-violet-500/10 dark:text-violet-300'
    )
    expect(platformBorderClass('grok')).toBe('border-zinc-800/20 dark:border-zinc-500/20')
    expect(platformTextClass('openai')).toBe('text-emerald-600 dark:text-emerald-400')
    expect(platformIconClass('opencode_go')).toBe('text-amber-500 dark:text-amber-300')
    expect(platformAccentColor('grok')).toBe('#71717a')
    expect(platformAccentColor('command_code')).toBe('#737373')
  })
})
