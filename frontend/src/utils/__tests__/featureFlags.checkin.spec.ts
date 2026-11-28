import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { FeatureFlags, resolveFeatureFlag } from '../featureFlags'

const DummyRouteView = { template: '<div />' }

vi.mock('@/views/HomeView.vue', () => ({ default: DummyRouteView }))
vi.mock('@/views/auth/LoginView.vue', () => ({ default: DummyRouteView }))
vi.mock('@/views/user/DashboardView.vue', () => ({ default: DummyRouteView }))
vi.mock('@/views/user/CheckinView.vue', () => ({ default: DummyRouteView }))
vi.mock('@/views/admin/DashboardView.vue', () => ({ default: DummyRouteView }))
vi.mock('@/views/admin/CheckinView.vue', () => ({ default: DummyRouteView }))

vi.mock('@/api/auth', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/auth')>()
  return {
    ...actual,
    getPublicSettings: vi.fn().mockResolvedValue({
      checkin_enabled: false
    }),
    authAPI: {
      ...actual.authAPI,
      getCurrentUser: vi.fn().mockResolvedValue({
        data: {
          id: 1,
          username: 'u1',
          email: 'u1@example.com',
          role: 'user',
          balance: 0,
          concurrency: 1,
          status: 'active',
          allowed_groups: null,
          balance_notify_enabled: false,
          balance_notify_threshold: null,
          balance_notify_extra_emails: [],
          created_at: '2026-01-01T00:00:00Z',
          updated_at: '2026-01-01T00:00:00Z',
          run_mode: 'simple'
        }
      })
    }
  }
})

describe('FeatureFlags.checkin & router guards', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('defines checkin as an opt-in flag backed by checkin_enabled', () => {
    expect(FeatureFlags.checkin.key).toBe('checkin_enabled')
    expect(FeatureFlags.checkin.mode).toBe('opt-in')

    expect(resolveFeatureFlag(null, FeatureFlags.checkin)).toBe(false)
    expect(resolveFeatureFlag({ checkin_enabled: false }, FeatureFlags.checkin)).toBe(false)
    expect(resolveFeatureFlag({ checkin_enabled: true }, FeatureFlags.checkin)).toBe(true)
  })

  it('guards /checkin route when checkin_enabled is false or in simple mode', async () => {
    const { default: router } = await import('@/router')
    const { useAppStore, useAuthStore } = await import('@/stores')

    const appStore = useAppStore()
    const authStore = useAuthStore()

    // Simulate authenticated standard user
    authStore.$patch({
      token: 'test-token',
      user: {
        id: 1,
        username: 'u1',
        email: 'u1@example.com',
        role: 'user',
        balance: 0,
        concurrency: 1,
        status: 'active',
        allowed_groups: null,
        balance_notify_enabled: false,
        balance_notify_threshold: null,
        balance_notify_extra_emails: [],
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-01T00:00:00Z',
        run_mode: 'standard'
      } as never
    })

    // 1. checkin_enabled = false -> redirects to /dashboard
    appStore.$patch({
      publicSettingsLoaded: true,
      cachedPublicSettings: { checkin_enabled: false } as never
    })
    await router.push('/checkin')
    expect(router.currentRoute.value.path).toBe('/dashboard')

    // 2. checkin_enabled = true & standard mode -> allows /checkin
    appStore.$patch({
      publicSettingsLoaded: true,
      cachedPublicSettings: { checkin_enabled: true } as never
    })
    await router.push('/checkin')
    expect(router.currentRoute.value.path).toBe('/checkin')

    // 3. checkin_enabled = true & simple mode -> redirects to /dashboard
    await router.push('/dashboard')
    await authStore.refreshUser()
    expect(authStore.isSimpleMode).toBe(true)
    await router.push('/checkin')
    expect(router.currentRoute.value.path).toBe('/dashboard')
  })
})
