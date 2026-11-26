import { beforeEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '@/api/client'
import * as adminRedeemAPI from '@/api/admin/redeem'

vi.mock('@/api/client', () => ({
  apiClient: {
    post: vi.fn()
  }
}))

describe('admin redeem API template & redeem_mode support', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(apiClient.post).mockResolvedValue({ data: [] } as any)
  })

  it('sends template_id and redeem_mode (default stack) when generating template subscription codes', async () => {
    await adminRedeemAPI.generate(5, 'subscription', 0, undefined, 30, 7, {
      templateId: 12,
      redeemMode: 'stack'
    })

    expect(apiClient.post).toHaveBeenCalledWith('/admin/redeem-codes/generate', {
      count: 5,
      type: 'subscription',
      value: 0,
      template_id: 12,
      redeem_mode: 'stack',
      validity_days: 30,
      expires_in_days: 7
    })
  })

  it('supports extend mode for template subscription codes', async () => {
    await adminRedeemAPI.generate(2, 'subscription', 0, undefined, 15, undefined, {
      templateId: 12,
      redeemMode: 'extend'
    })

    expect(apiClient.post).toHaveBeenCalledWith('/admin/redeem-codes/generate', {
      count: 2,
      type: 'subscription',
      value: 0,
      template_id: 12,
      redeem_mode: 'extend',
      validity_days: 15
    })
  })

  it('preserves legacy group_id payload when no template is specified', async () => {
    await adminRedeemAPI.generate(3, 'subscription', 0, 42, 30)

    expect(apiClient.post).toHaveBeenCalledWith('/admin/redeem-codes/generate', {
      count: 3,
      type: 'subscription',
      value: 0,
      group_id: 42,
      validity_days: 30
    })
  })
})
