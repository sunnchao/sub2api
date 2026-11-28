import { afterEach, describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import { BUILTIN_PLATFORM_ICON_KEYS } from '@/constants/platformIcons'
import {
  BUILTIN_PLATFORM_CATALOG,
  resetPlatformCatalog,
  setPlatformCatalog,
  type PlatformSpec
} from '@/constants/platformCatalog'

function mountCustom(spec: PlatformSpec) {
  setPlatformCatalog({
    platforms: [...BUILTIN_PLATFORM_CATALOG.platforms, spec],
    composite_precedence: BUILTIN_PLATFORM_CATALOG.composite_precedence
  })
  return mount(PlatformIcon, { props: { platform: spec.id, size: 'sm' } })
}

const customBase = {
  gateway: 'openai' as const,
  cn_provider: false,
  custom: true
}

describe('PlatformIcon custom platform rendering', () => {
  afterEach(() => {
    resetPlatformCatalog()
  })

  it('renders an http icon through the URL sanitizer', () => {
    const wrapper = mountCustom({
      ...customBase,
      id: 'acme_router',
      display_name: 'Acme Router',
      icon: { type: 'url', value: 'https://cdn.example.com/acme.png' }
    })

    const img = wrapper.find('img')
    expect(img.exists()).toBe(true)
    expect(img.attributes('src')).toBe('https://cdn.example.com/acme.png')
    expect(img.attributes('referrerpolicy')).toBe('no-referrer')
    expect(img.classes()).toContain('object-contain')
    // Must render a single icon node (no fallback SVG rendered alongside custom icon).
    expect(wrapper.findAll('img, svg, [data-testid="platform-icon-letter"]')).toHaveLength(1)
  })

  it('renders a site-relative icon path as an img without fallback SVG', () => {
    const wrapper = mountCustom({
      ...customBase,
      id: 'local_logo',
      display_name: 'Local Logo',
      icon: { type: 'url', value: '/logos/custom.png' }
    })

    const img = wrapper.find('img')
    expect(img.exists()).toBe(true)
    expect(img.attributes('src')).toBe('/logos/custom.png')
    expect(wrapper.findAll('img, svg, [data-testid="platform-icon-letter"]')).toHaveLength(1)
  })

  it('renders a data URL icon', () => {
    const dataUrl = 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUg=='
    const wrapper = mountCustom({
      ...customBase,
      id: 'data_icon',
      display_name: 'Data Icon',
      icon: { type: 'data_url', value: dataUrl }
    })

    expect(wrapper.find('img').attributes('src')).toBe(dataUrl)
  })

  it('rejects javascript: URLs and falls back to the letter badge', () => {
    const wrapper = mountCustom({
      ...customBase,
      id: 'evil_logo',
      display_name: 'Evil Logo',
      icon: { type: 'url', value: 'javascript:alert(1)' }
    })

    expect(wrapper.find('img').exists()).toBe(false)
    const badge = wrapper.find('[data-testid="platform-icon-letter"]')
    expect(badge.exists()).toBe(true)
    expect(badge.text()).toBe('E')
    expect(wrapper.html()).not.toContain('javascript:alert(1)')
  })

  it('renders a letter badge with the platform color preset', () => {
    const wrapper = mountCustom({
      ...customBase,
      id: 'letter_router',
      display_name: 'letter router',
      color_preset: 'teal',
      icon: { type: 'letter', value: 'l' }
    })

    const badge = wrapper.find('[data-testid="platform-icon-letter"]')
    expect(badge.text()).toBe('L')
    expect(badge.classes()).toContain('bg-teal-500/10')
    expect(badge.classes()).toContain('border-teal-500/30')
    expect(badge.classes()).toContain('rounded')
  })

  it('uses the display name initial when a custom platform has no icon', () => {
    const wrapper = mountCustom({
      ...customBase,
      id: 'plain_router',
      display_name: 'SiliconFlow'
    })

    expect(wrapper.find('[data-testid="platform-icon-letter"]').text()).toBe('S')
  })

  it('renders all 8 whitelisted generic builtin icons with distinct monochrome paths', () => {
    const paths = new Set<string>()
    for (const iconKey of BUILTIN_PLATFORM_ICON_KEYS) {
      const wrapper = mountCustom({
        ...customBase,
        id: `builtin_${iconKey}`,
        display_name: `Builtin ${iconKey}`,
        icon: { type: 'builtin', value: iconKey }
      })

      const svg = wrapper.find('[data-testid="platform-icon-generic"]')
      expect(svg.exists()).toBe(true)
      expect(svg.attributes('stroke')).toBe('currentColor')
      const pathD = svg.find('path').attributes('d') ?? ''
      expect(pathD).toBeTruthy()
      paths.add(pathD)
      expect(wrapper.findAll('img, svg, [data-testid="platform-icon-letter"]')).toHaveLength(1)
      wrapper.unmount()
    }
    expect(paths.size).toBe(BUILTIN_PLATFORM_ICON_KEYS.length)
  })

  it('falls back to a generic icon for unknown builtin values', () => {
    const wrapper = mountCustom({
      ...customBase,
      id: 'unknown_builtin',
      display_name: 'Unknown Builtin',
      icon: { type: 'builtin', value: 'not-a-real-icon' }
    })

    expect(wrapper.find('[data-testid="platform-icon-generic"]').exists()).toBe(true)
  })

  it('swaps a broken image for the letter badge', async () => {
    const wrapper = mountCustom({
      ...customBase,
      id: 'broken_logo',
      display_name: 'Broken Logo',
      icon: { type: 'url', value: 'https://cdn.example.com/broken.png' }
    })

    await wrapper.find('img').trigger('error')

    expect(wrapper.find('img').exists()).toBe(false)
    expect(wrapper.find('[data-testid="platform-icon-letter"]').text()).toBe('B')
  })

  it('keeps builtin platform logos untouched', () => {
    const wrapper = mount(PlatformIcon, { props: { platform: 'gemini', size: 'sm' } })

    expect(wrapper.find('img').exists()).toBe(false)
    expect(wrapper.find('[data-testid="platform-icon-letter"]').exists()).toBe(false)
    expect(wrapper.find('svg').exists()).toBe(true)
    expect(wrapper.find('svg').attributes('viewBox')).toBe('0 0 24 24')
  })
})
