import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import AiEcosystemConstellation from '../AiEcosystemConstellation.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

describe('AiEcosystemConstellation', () => {
  beforeEach(() => {
    vi.spyOn(window, 'matchMedia').mockReturnValue({ matches: true } as MediaQueryList)
  })

  it('renders the hero title, central particle orb, all five branches, leaves, and tagline', () => {
    const wrapper = mount(AiEcosystemConstellation)

    expect(wrapper.text()).toContain('home.ecosystem.title')
    expect(wrapper.text()).toContain('home.ecosystem.centerTitle')
    expect(wrapper.text()).toContain('home.ecosystem.tagline')
    expect(wrapper.text()).toContain('home.ecosystem.footerHint')
    expect(wrapper.find('[data-testid="ecosystem-center-orb"] canvas').exists()).toBe(true)

    for (const id of ['models', 'multimodal', 'usage', 'production', 'integration']) {
      expect(wrapper.find(`[data-testid="ecosystem-branch-${id}"]`).exists()).toBe(true)
    }
    expect(wrapper.text()).toContain('home.ecosystem.branches.models.leaf1')
    expect(wrapper.text()).toContain('home.ecosystem.branches.multimodal.leaf4')
    expect(wrapper.text()).toContain('home.ecosystem.branches.production.leaf3')
  })

  it('switches active spotlight branch on hover/click and cycles when clicking the center orb', async () => {
    const wrapper = mount(AiEcosystemConstellation)

    const modelsBtn = wrapper.get('[data-testid="ecosystem-branch-models"]')
    const multimodalBtn = wrapper.get('[data-testid="ecosystem-branch-multimodal"]')
    const productionBtn = wrapper.get('[data-testid="ecosystem-branch-production"]')

    expect(modelsBtn.attributes('aria-pressed')).toBe('true')

    await productionBtn.trigger('mouseenter')
    expect(productionBtn.attributes('aria-pressed')).toBe('true')
    expect(modelsBtn.attributes('aria-pressed')).toBe('false')

    await multimodalBtn.trigger('click')
    expect(multimodalBtn.attributes('aria-pressed')).toBe('true')

    await wrapper.get('[data-testid="ecosystem-center-orb"]').trigger('click')
    expect(wrapper.get('[data-testid="ecosystem-branch-usage"]').attributes('aria-pressed')).toBe('true')
  })
})
