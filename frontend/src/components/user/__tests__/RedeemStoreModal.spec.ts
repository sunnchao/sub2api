import { afterEach, describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enLocale from '@/i18n/locales/en'
import RedeemStoreModal from '../RedeemStoreModal.vue'

function mountModal(props: {
  show: boolean
  url: string
  title?: string
  showRedeemAction?: boolean
}) {
  const i18n = createI18n({
    legacy: false,
    locale: 'en',
    messages: { en: enLocale },
  })
  return mount(RedeemStoreModal, {
    props,
    attachTo: document.body,
    global: {
      plugins: [i18n],
      stubs: {
        Teleport: true,
        Transition: false,
        Icon: true,
      },
    },
  })
}

describe('RedeemStoreModal', () => {
  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('embeds the raw URL with no-referrer and opens external link safely', () => {
    const wrapper = mountModal({
      show: true,
      url: 'https://shop.example.com/buy?ref=1',
      title: 'Card Store',
      showRedeemAction: true,
    })

    const iframe = wrapper.get('[data-testid="redeem-store-iframe"]')
    expect(iframe.attributes('src')).toBe('https://shop.example.com/buy?ref=1')
    expect(iframe.attributes('referrerpolicy')).toBe('no-referrer')

    const external = wrapper.get('[data-testid="redeem-store-modal-open-external"]')
    expect(external.attributes('href')).toBe('https://shop.example.com/buy?ref=1')
    expect(external.attributes('target')).toBe('_blank')
    expect(external.attributes('rel')).toBe('noopener noreferrer')
    expect(wrapper.text()).toContain('Card Store')

    expect(wrapper.find('[data-testid="redeem-store-modal-go-redeem"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('does not render modal for unsafe URLs', () => {
    const wrapper = mountModal({
      show: true,
      url: 'javascript:alert(1)',
    })
    expect(wrapper.find('[data-testid="redeem-store-modal"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('emits close on close button, ESC key, and emits go-redeem', async () => {
    const wrapper = mountModal({
      show: true,
      url: 'https://shop.example.com',
      showRedeemAction: true,
    })

    await wrapper.get('[data-testid="redeem-store-modal-go-redeem"]').trigger('click')
    expect(wrapper.emitted('close')).toHaveLength(1)
    expect(wrapper.emitted('go-redeem')).toHaveLength(1)

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    expect(wrapper.emitted('close')).toHaveLength(2)

    await wrapper.get('[data-testid="redeem-store-modal-close"]').trigger('click')
    expect(wrapper.emitted('close')).toHaveLength(3)
    wrapper.unmount()
  })
})
