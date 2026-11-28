import type { User } from '@/types'

export interface ResolvedUserBalance {
  regularBalance: number
  expiringBalance: number
  totalAvailableBalance: number
  frozenRegularBalance: number
  expiringFrozenBalance: number
  totalFrozenBalance: number
  totalBalance: number
  nearestExpiringAt: string | null
  nearestExpiringAmount: number
}

function toFiniteNumber(val: number | null | undefined): number {
  const num = Number(val)
  return Number.isFinite(num) ? num : 0
}

export function resolveUserBalance(user: Partial<User> | null | undefined): ResolvedUserBalance {
  const regularBalance = toFiniteNumber(user?.balance)
  const expiringBalance = Math.max(0, toFiniteNumber(user?.expiring_balance))
  const totalAvailableBalance =
    user?.total_available_balance != null && Number.isFinite(Number(user.total_available_balance))
      ? Number(user.total_available_balance)
      : regularBalance + expiringBalance
  const frozenRegularBalance = Math.max(0, toFiniteNumber(user?.frozen_balance))
  const expiringFrozenBalance = Math.max(0, toFiniteNumber(user?.expiring_frozen_balance))
  const totalFrozenBalance = frozenRegularBalance + expiringFrozenBalance
  const totalBalance = totalAvailableBalance + totalFrozenBalance
  const nearestExpiringAt = user?.nearest_expiring_at ?? null
  const nearestExpiringAmount = Math.max(0, toFiniteNumber(user?.nearest_expiring_amount))

  return {
    regularBalance,
    expiringBalance,
    totalAvailableBalance,
    frozenRegularBalance,
    expiringFrozenBalance,
    totalFrozenBalance,
    totalBalance,
    nearestExpiringAt,
    nearestExpiringAmount
  }
}
