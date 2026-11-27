import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, RouterLinkStub } from '@vue/test-utils'
import AuthLayout from '../AuthLayout.vue'

const { appStore } = vi.hoisted(() => ({
  appStore: {
    siteName: 'Sub2API',
    siteLogo: '',
    cachedPublicSettings: { site_subtitle: 'Gateway' } as Record<string, unknown>,
    publicSettingsLoaded: true,
    fetchPublicSettings: vi.fn(),
  },
}))

vi.mock('@/stores', () => ({
  useAppStore: () => appStore,
}))

describe('AuthLayout logo navigation', () => {
  beforeEach(() => {
    appStore.fetchPublicSettings.mockClear()
  })

  it('wraps the login page logo and brand name in a link to /home', () => {
    const wrapper = mount(AuthLayout, {
      global: {
        stubs: {
          RouterLink: RouterLinkStub,
        },
      },
      slots: {
        default: '<div>Login Form</div>',
      },
    })

    const link = wrapper.getComponent(RouterLinkStub)
    expect(link.props('to')).toBe('/home')
    expect(link.find('img[alt="Logo"]').exists()).toBe(true)
    expect(link.text()).toContain('Sub2API')
  })
})
