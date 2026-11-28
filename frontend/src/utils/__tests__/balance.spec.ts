import { describe, expect, it } from 'vitest'
import { resolveUserBalance } from '../balance'

describe('resolveUserBalance', () => {
  it('returns zeroed defaults when user is null or undefined', () => {
    expect(resolveUserBalance(null)).toEqual({
      regularBalance: 0,
      expiringBalance: 0,
      totalAvailableBalance: 0,
      frozenRegularBalance: 0,
      expiringFrozenBalance: 0,
      totalFrozenBalance: 0,
      totalBalance: 0,
      nearestExpiringAt: null,
      nearestExpiringAmount: 0
    })
    expect(resolveUserBalance(undefined).totalAvailableBalance).toBe(0)
  })

  it('preserves legacy behavior when expiring fields are absent', () => {
    const res = resolveUserBalance({
      balance: 12.5,
      frozen_balance: 1.5
    })
    expect(res.regularBalance).toBe(12.5)
    expect(res.expiringBalance).toBe(0)
    expect(res.totalAvailableBalance).toBe(12.5)
    expect(res.totalFrozenBalance).toBe(1.5)
    expect(res.totalBalance).toBe(14)
    expect(res.nearestExpiringAt).toBeNull()
  })

  it('computes total available and total frozen balances when expiring fields are present', () => {
    const res = resolveUserBalance({
      balance: 10,
      frozen_balance: 2,
      expiring_balance: 3.5,
      expiring_frozen_balance: 0.5,
      nearest_expiring_at: '2026-11-27T23:59:59Z',
      nearest_expiring_amount: 1.25
    })
    expect(res.regularBalance).toBe(10)
    expect(res.expiringBalance).toBe(3.5)
    expect(res.totalAvailableBalance).toBe(13.5)
    expect(res.frozenRegularBalance).toBe(2)
    expect(res.expiringFrozenBalance).toBe(0.5)
    expect(res.totalFrozenBalance).toBe(2.5)
    expect(res.totalBalance).toBe(16)
    expect(res.nearestExpiringAt).toBe('2026-11-27T23:59:59Z')
    expect(res.nearestExpiringAmount).toBe(1.25)
  })

  it('prefers explicit total_available_balance when provided by backend', () => {
    const res = resolveUserBalance({
      balance: 10,
      expiring_balance: 2,
      total_available_balance: 11.75
    })
    expect(res.totalAvailableBalance).toBe(11.75)
  })
})
