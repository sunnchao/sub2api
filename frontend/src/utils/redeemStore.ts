import type { PublicSettings } from '@/types'
import { FeatureFlags, resolveFeatureFlag } from '@/utils/featureFlags'
import { resolveSiteBillingMode, type SiteBillingMode } from '@/utils/siteBillingMode'

export type RedeemStoreOpenMode = 'new_tab' | 'iframe'

export const REDEEM_STORE_MAX_URL_LENGTH = 2048
export const REDEEM_STORE_MAX_LABEL_LENGTH = 50

export interface RedeemStoreConfig {
  enabled: boolean
  url: string
  openMode: RedeemStoreOpenMode
  label: string
}

/**
 * Validates that a redeem store URL is an absolute http:// or https:// URL.
 */
export function isValidRedeemStoreUrl(raw: string | null | undefined): boolean {
  const trimmed = typeof raw === 'string' ? raw.trim() : ''
  if (!trimmed || trimmed.length > REDEEM_STORE_MAX_URL_LENGTH) {
    return false
  }
  try {
    const parsed = new URL(trimmed)
    return (parsed.protocol === 'http:' || parsed.protocol === 'https:') && Boolean(parsed.host)
  } catch {
    return false
  }
}

/**
 * Returns the trimmed redeem store URL if valid, otherwise an empty string.
 */
export function normalizeRedeemStoreUrl(raw: string | null | undefined): string {
  const trimmed = typeof raw === 'string' ? raw.trim() : ''
  return isValidRedeemStoreUrl(trimmed) ? trimmed : ''
}

/**
 * Normalizes open mode to either 'iframe' or 'new_tab' (default).
 */
export function normalizeRedeemStoreOpenMode(raw: string | null | undefined): RedeemStoreOpenMode {
  return typeof raw === 'string' && raw.trim() === 'iframe' ? 'iframe' : 'new_tab'
}

/**
 * Resolves the effective redeem store configuration from public settings.
 */
export function resolveRedeemStoreConfig(
  settings: Partial<PublicSettings> | null | undefined,
): RedeemStoreConfig {
  const url = normalizeRedeemStoreUrl(settings?.redeem_store_url)
  const openMode = normalizeRedeemStoreOpenMode(settings?.redeem_store_open_mode)
  const label = typeof settings?.redeem_store_label === 'string'
    ? settings.redeem_store_label.trim()
    : ''
  return {
    enabled: url.length > 0,
    url,
    openMode,
    label,
  }
}

/**
 * Returns true when a valid redeem store URL is configured.
 */
export function hasRedeemStoreEntry(
  settings: Partial<PublicSettings> | null | undefined,
): boolean {
  return resolveRedeemStoreConfig(settings).enabled
}

/**
 * Determines whether the user-facing /purchase route and sidebar entry should be accessible.
 * Accessible when internal online payment is enabled OR when a valid redeem store URL is set.
 */
export function canAccessPurchasePage(
  settings: Partial<PublicSettings> | null | undefined,
): boolean {
  return resolveFeatureFlag(settings, FeatureFlags.payment) || hasRedeemStoreEntry(settings)
}

/**
 * Resolves the billing mode label used for /purchase navigation and page header.
 * When online payment is explicitly disabled and /purchase is only open for the redeem store,
 * forces 'recharge_only' so the UI displays "充值 / Top Up" rather than "充值/订阅" or "订阅".
 */
export function resolvePurchaseDisplayBillingMode(
  settings: Partial<PublicSettings> | null | undefined,
): SiteBillingMode {
  if (settings?.payment_enabled === false && hasRedeemStoreEntry(settings)) {
    return 'recharge_only'
  }
  return resolveSiteBillingMode(settings)
}
