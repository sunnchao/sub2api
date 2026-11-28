import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import enLocale from '@/i18n/locales/en'
import CheckinSettingsSection from '../CheckinSettingsSection.vue'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores'

vi.mock('@/api/admin', () => ({
  adminAPI: {
    checkin: {
      getConfig: vi.fn(),
      updateConfig: vi.fn()
    }
  }
}))

describe('CheckinSettingsSection', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  function mountSection(captchaConfigured = false) {
    const i18n = createI18n({
      legacy: false,
      locale: 'en',
      messages: { en: enLocale }
    })
    return mount(CheckinSettingsSection, {
      props: { captchaConfigured },
      global: {
        plugins: [i18n],
        stubs: {
          RouterLink: { template: '<a><slot /></a>' }
        }
      }
    })
  }

  it('handles failed getConfig gracefully on mount without throwing', async () => {
    vi.mocked(adminAPI.checkin.getConfig).mockRejectedValueOnce(new Error('404 Not Found'))
    const wrapper = mountSection(false)
    await flushPromises()
    expect(wrapper.find('[data-testid="checkin-save-btn"]').exists()).toBe(true)
  })

  it('shows warning when captcha is enabled without configured captcha provider', async () => {
    vi.mocked(adminAPI.checkin.getConfig).mockResolvedValue({
      enabled: true,
      reward_mode: 'fixed',
      base_amount: 0.1,
      min_amount: 0.05,
      max_amount: 0.2,
      amount_precision: 2,
      validity_days: 1,
      milestones: [],
      milestone_cycle_repeat: false,
      eligibility: {
        min_account_age_days: 0,
        min_total_recharged: 0,
        max_available_balance: 0,
        allowed_signup_sources: []
      },
      risk: {
        captcha_enabled: true,
        max_accounts_per_ip_day: 0,
        daily_total_budget_usd: 0
      },
      rules_description: ''
    })

    const wrapper = mountSection(false)
    await flushPromises()
    expect(wrapper.find('[data-testid="checkin-captcha-warning"]').exists()).toBe(true)

    await wrapper.setProps({ captchaConfigured: true })
    expect(wrapper.find('[data-testid="checkin-captcha-warning"]').exists()).toBe(false)
  })

  it('saves config with type=button, calls fetchPublicSettings(true), and displays server warnings', async () => {
    const appStore = useAppStore()
    const fetchSpy = vi.spyOn(appStore, 'fetchPublicSettings').mockResolvedValue(null)

    vi.mocked(adminAPI.checkin.getConfig).mockResolvedValue({
      enabled: false,
      reward_mode: 'fixed',
      base_amount: 0.1,
      min_amount: 0.05,
      max_amount: 0.2,
      amount_precision: 2,
      validity_days: 1,
      milestones: [],
      milestone_cycle_repeat: false,
      eligibility: {
        min_account_age_days: 0,
        min_total_recharged: 0,
        max_available_balance: 0,
        allowed_signup_sources: []
      },
      risk: {
        captcha_enabled: false,
        max_accounts_per_ip_day: 0,
        daily_total_budget_usd: 0
      },
      rules_description: ''
    })
    vi.mocked(adminAPI.checkin.updateConfig).mockResolvedValue({
      enabled: true,
      reward_mode: 'fixed',
      base_amount: 0.1,
      min_amount: 0.05,
      max_amount: 0.2,
      amount_precision: 2,
      validity_days: 1,
      milestones: [{ days: 3, bonus_amount: 0.5 }],
      milestone_cycle_repeat: false,
      eligibility: {
        min_account_age_days: 0,
        min_total_recharged: 0,
        max_available_balance: 0,
        allowed_signup_sources: []
      },
      risk: {
        captcha_enabled: true,
        max_accounts_per_ip_day: 0,
        daily_total_budget_usd: 0
      },
      rules_description: '',
      warnings: ['No captcha provider enabled']
    })

    const wrapper = mountSection(true)
    await flushPromises()

    const saveBtn = wrapper.find('[data-testid="checkin-save-btn"]')
    expect(saveBtn.attributes('type')).toBe('button')

    await wrapper.find('[data-testid="add-milestone-btn"]').trigger('click')
    await saveBtn.trigger('click')
    await flushPromises()

    expect(adminAPI.checkin.updateConfig).toHaveBeenCalled()
    expect(fetchSpy).toHaveBeenCalledWith(true)
    expect(wrapper.find('[data-testid="checkin-server-warnings"]').text()).toContain(
      'No captcha provider enabled'
    )
  })

  it('rejects min_amount === 0 in random reward mode', async () => {
    vi.mocked(adminAPI.checkin.getConfig).mockResolvedValue({
      enabled: true,
      reward_mode: 'random',
      base_amount: 0.1,
      min_amount: 0,
      max_amount: 0.5,
      amount_precision: 2,
      validity_days: 1,
      milestones: [],
      milestone_cycle_repeat: false,
      eligibility: {
        min_account_age_days: 0,
        min_total_recharged: 0,
        max_available_balance: 0,
        allowed_signup_sources: []
      },
      risk: {
        captcha_enabled: false,
        max_accounts_per_ip_day: 0,
        daily_total_budget_usd: 0
      },
      rules_description: ''
    })

    const wrapper = mountSection(false)
    await flushPromises()

    await wrapper.find('[data-testid="checkin-save-btn"]').trigger('click')
    await flushPromises()

    expect(adminAPI.checkin.updateConfig).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('min > 0')
  })

  it('shows success even when fetchPublicSettings(true) fails after updateConfig succeeds', async () => {
    const appStore = useAppStore()
    vi.spyOn(appStore, 'fetchPublicSettings').mockRejectedValue(new Error('transient error'))
    const successSpy = vi.spyOn(appStore, 'showSuccess')
    const errorSpy = vi.spyOn(appStore, 'showError')

    const baseCfg = {
      enabled: true,
      reward_mode: 'fixed' as const,
      base_amount: 0.1,
      min_amount: 0.05,
      max_amount: 0.2,
      amount_precision: 2,
      validity_days: 1,
      milestones: [],
      milestone_cycle_repeat: false,
      eligibility: {
        min_account_age_days: 0,
        min_total_recharged: 0,
        max_available_balance: 0,
        allowed_signup_sources: []
      },
      risk: {
        captcha_enabled: false,
        max_accounts_per_ip_day: 0,
        daily_total_budget_usd: 0
      },
      rules_description: ''
    }
    vi.mocked(adminAPI.checkin.getConfig).mockResolvedValue(baseCfg)
    vi.mocked(adminAPI.checkin.updateConfig).mockResolvedValue(baseCfg)

    const wrapper = mountSection(true)
    await flushPromises()

    await wrapper.find('[data-testid="checkin-save-btn"]').trigger('click')
    await flushPromises()

    expect(adminAPI.checkin.updateConfig).toHaveBeenCalled()
    expect(successSpy).toHaveBeenCalled()
    expect(errorSpy).not.toHaveBeenCalled()
  })
})
