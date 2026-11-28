import { describe, expect, it } from 'vitest'
import {
  canAccessPurchasePage,
  hasRedeemStoreEntry,
  isValidRedeemStoreUrl,
  normalizeRedeemStoreOpenMode,
  normalizeRedeemStoreUrl,
  resolvePurchaseDisplayBillingMode,
  resolveRedeemStoreConfig,
} from '../redeemStore'

describe('redeemStore utilities', () => {
  it('validates absolute http(s) URLs only', () => {
    expect(isValidRedeemStoreUrl('https://shop.example.com/cards?a=1')).toBe(true)
    expect(isValidRedeemStoreUrl('http://localhost:8080/store')).toBe(true)
    expect(isValidRedeemStoreUrl('  https://shop.example.com  ')).toBe(true)

    expect(isValidRedeemStoreUrl('')).toBe(false)
    expect(isValidRedeemStoreUrl('   ')).toBe(false)
    expect(isValidRedeemStoreUrl('javascript:alert(1)')).toBe(false)
    expect(isValidRedeemStoreUrl('ftp://shop.example.com')).toBe(false)
    expect(isValidRedeemStoreUrl('/relative/path')).toBe(false)
    expect(isValidRedeemStoreUrl('shop.example.com')).toBe(false)
  })

  it('normalizes URL, open mode, and label cleanly', () => {
    expect(normalizeRedeemStoreUrl('  https://shop.example.com/store  ')).toBe('https://shop.example.com/store')
    expect(normalizeRedeemStoreUrl('javascript:alert(1)')).toBe('')

    expect(normalizeRedeemStoreOpenMode('iframe')).toBe('iframe')
    expect(normalizeRedeemStoreOpenMode('new_tab')).toBe('new_tab')
    expect(normalizeRedeemStoreOpenMode('')).toBe('new_tab')
    expect(normalizeRedeemStoreOpenMode('popup')).toBe('new_tab')

    expect(resolveRedeemStoreConfig({
      redeem_store_url: '  https://shop.example.com  ',
      redeem_store_open_mode: 'iframe',
      redeem_store_label: '  官方发卡  ',
    })).toEqual({
      enabled: true,
      url: 'https://shop.example.com',
      openMode: 'iframe',
      label: '官方发卡',
    })

    expect(resolveRedeemStoreConfig({ redeem_store_url: '' })).toEqual({
      enabled: false,
      url: '',
      openMode: 'new_tab',
      label: '',
    })
  })

  it('grants /purchase access when payment is enabled OR redeem store is configured', () => {
    expect(canAccessPurchasePage({ payment_enabled: true })).toBe(true)
    expect(canAccessPurchasePage({ payment_enabled: false, redeem_store_url: '' })).toBe(false)
    expect(canAccessPurchasePage({ payment_enabled: false, redeem_store_url: 'https://shop.example.com' })).toBe(true)
    expect(hasRedeemStoreEntry({ redeem_store_url: 'https://shop.example.com' })).toBe(true)
    expect(hasRedeemStoreEntry({ redeem_store_url: 'javascript:void(0)' })).toBe(false)
  })

  it('forces recharge_only billing mode label when online payment is disabled and redeem store is set', () => {
    expect(resolvePurchaseDisplayBillingMode({
      payment_enabled: false,
      subscription_enabled: true,
      payment_balance_disabled: true,
      redeem_store_url: 'https://shop.example.com',
    })).toBe('recharge_only')

    expect(resolvePurchaseDisplayBillingMode({
      payment_enabled: true,
      subscription_enabled: true,
      payment_balance_disabled: true,
      redeem_store_url: 'https://shop.example.com',
    })).toBe('subscription_only')
  })
})
