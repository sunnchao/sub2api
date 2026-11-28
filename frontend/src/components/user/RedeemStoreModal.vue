<template>
  <Teleport to="body">
    <Transition name="modal">
      <div
        v-if="show && safeUrl"
        class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-3 backdrop-blur-sm sm:p-5"
        role="dialog"
        aria-modal="true"
        :aria-label="modalTitle"
        data-testid="redeem-store-modal"
        @click.self="emit('close')"
      >
        <div
          class="flex h-[85vh] w-full max-w-5xl flex-col overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-2xl dark:border-dark-700 dark:bg-dark-900"
        >
          <!-- Modal Header -->
          <div
            class="flex shrink-0 flex-wrap items-center justify-between gap-2 border-b border-gray-200 px-4 py-3 dark:border-dark-700 sm:px-5"
          >
            <div class="flex min-w-0 items-center gap-2">
              <Icon name="gift" size="md" class="shrink-0 text-primary-600 dark:text-primary-400" />
              <h3 class="truncate text-base font-semibold text-gray-900 dark:text-white">
                {{ modalTitle }}
              </h3>
            </div>
            <div class="flex items-center gap-2">
              <button
                v-if="showRedeemAction"
                type="button"
                class="btn btn-primary btn-sm"
                data-testid="redeem-store-modal-go-redeem"
                @click="handleGoRedeem"
              >
                {{ t('redeem.goToRedeem') }}
              </button>
              <a
                :href="safeUrl"
                target="_blank"
                rel="noopener noreferrer"
                class="btn btn-secondary btn-sm inline-flex items-center gap-1"
                data-testid="redeem-store-modal-open-external"
              >
                <Icon name="externalLink" size="xs" :stroke-width="2" />
                <span>{{ t('redeem.openStoreInNewTab') }}</span>
              </a>
              <button
                type="button"
                class="rounded-lg p-1.5 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-dark-700 dark:hover:text-gray-200"
                :aria-label="t('common.close')"
                data-testid="redeem-store-modal-close"
                @click="emit('close')"
              >
                <Icon name="x" size="md" />
              </button>
            </div>
          </div>

          <!-- Iframe Container -->
          <div class="relative flex-1 bg-gray-50 dark:bg-dark-950">
            <div
              v-if="frameLoading"
              class="pointer-events-none absolute inset-0 flex items-center justify-center bg-white/70 dark:bg-dark-900/70"
            >
              <div
                class="h-8 w-8 animate-spin rounded-full border-4 border-primary-500 border-t-transparent"
              ></div>
            </div>
            <iframe
              :src="safeUrl"
              :title="modalTitle"
              class="h-full w-full border-0"
              referrerpolicy="no-referrer"
              allowfullscreen
              data-testid="redeem-store-iframe"
              @load="frameLoading = false"
            />
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { normalizeRedeemStoreUrl } from '@/utils/redeemStore'

const props = withDefaults(defineProps<{
  show: boolean
  url: string
  title?: string
  showRedeemAction?: boolean
}>(), {
  title: '',
  showRedeemAction: false,
})

const emit = defineEmits<{
  close: []
  'go-redeem': []
}>()

const { t } = useI18n()
const frameLoading = ref(true)

const safeUrl = computed(() => normalizeRedeemStoreUrl(props.url))
const modalTitle = computed(() => props.title?.trim() || t('redeem.buyRedeemCode'))

function handleKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape' && props.show) {
    emit('close')
  }
}

function handleGoRedeem() {
  emit('close')
  emit('go-redeem')
}

watch(
  () => [props.show, safeUrl.value] as const,
  ([visible, url]) => {
    if (visible && url) {
      frameLoading.value = true
      if (typeof window !== 'undefined') {
        window.addEventListener('keydown', handleKeydown)
      }
    } else if (typeof window !== 'undefined') {
      window.removeEventListener('keydown', handleKeydown)
    }
  },
  { immediate: true },
)

onUnmounted(() => {
  if (typeof window !== 'undefined') {
    window.removeEventListener('keydown', handleKeydown)
  }
})
</script>
