import { beforeEach, describe, expect, it, vi } from 'vitest'
import { checkinAPI } from '../checkin'
import { adminCheckinAPI } from '../admin/checkin'
import { isUserTimingAPIPath } from '../adminUIRequest'
import { apiClient } from '../client'

vi.mock('../client', () => ({
  apiClient: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn()
  }
}))

const mockedClient = vi.mocked(apiClient)

describe('checkinAPI & adminCheckinAPI', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('calls user check-in endpoints with expected methods and params', async () => {
    mockedClient.get.mockResolvedValue({ data: { enabled: true } } as never)
    await checkinAPI.getStatus()
    expect(mockedClient.get).toHaveBeenCalledWith('/checkin/status')

    mockedClient.post.mockResolvedValue({ data: { total_available_balance: 5 } } as never)
    await checkinAPI.checkin({ turnstile_token: 'tok-123' })
    expect(mockedClient.post).toHaveBeenCalledWith('/checkin', { turnstile_token: 'tok-123' })

    await checkinAPI.checkin({ tencent_captcha_ticket: 't1', tencent_captcha_randstr: 'r1' })
    expect(mockedClient.post).toHaveBeenCalledWith('/checkin', {
      tencent_captcha_ticket: 't1',
      tencent_captcha_randstr: 'r1'
    })

    await checkinAPI.getCalendar('2026-11')
    expect(mockedClient.get).toHaveBeenCalledWith('/checkin/calendar', {
      params: { month: '2026-11' }
    })

    await checkinAPI.getRecords(2, 15)
    expect(mockedClient.get).toHaveBeenCalledWith('/checkin/records', {
      params: { page: 2, page_size: 15 }
    })

    await checkinAPI.getLots(1, 10, 'active')
    expect(mockedClient.get).toHaveBeenCalledWith('/checkin/lots', {
      params: { page: 1, page_size: 10, status: 'active' }
    })
  })

  it('calls admin check-in endpoints and omits empty filter params', async () => {
    mockedClient.get.mockResolvedValue({ data: {} } as never)
    mockedClient.put.mockResolvedValue({ data: { enabled: true } } as never)

    await adminCheckinAPI.getConfig()
    expect(mockedClient.get).toHaveBeenCalledWith('/admin/checkin/config')

    await adminCheckinAPI.updateConfig({
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
        captcha_enabled: false,
        max_accounts_per_ip_day: 0,
        daily_total_budget_usd: 0
      },
      rules_description: ''
    })
    expect(mockedClient.put).toHaveBeenCalledWith('/admin/checkin/config', expect.objectContaining({ enabled: true }))

    await adminCheckinAPI.getStats(14)
    expect(mockedClient.get).toHaveBeenCalledWith('/admin/checkin/stats', { params: { days: 14 } })

    await adminCheckinAPI.listRecords({
      page: 1,
      page_size: 20,
      user_id: 42,
      search: 'alice',
      client_ip: '',
      start_date: '2026-11-01',
      end_date: ''
    })
    expect(mockedClient.get).toHaveBeenCalledWith('/admin/checkin/records', {
      params: {
        page: 1,
        page_size: 20,
        user_id: 42,
        search: 'alice',
        start_date: '2026-11-01'
      }
    })
  })

  it('marks /checkin paths as user timing API paths', () => {
    expect(isUserTimingAPIPath('/checkin')).toBe(true)
    expect(isUserTimingAPIPath('/api/v1/checkin/status')).toBe(true)
  })
})
