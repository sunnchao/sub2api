<template>
  <BaseDialog
    :show="show"
    :title="t('checkin.captcha.title')"
    width="narrow"
    @close="handleClose"
  >
    <div class="space-y-4">
      <p class="text-sm text-gray-600 dark:text-gray-300">
        {{ t('checkin.captcha.hint') }}
      </p>

      <div v-if="hasAnyProvider" class="flex justify-center">
        <CaptchaChallenge
          ref="captchaRef"
          :turnstile-enabled="turnstileEnabled"
          :turnstile-site-key="turnstileSiteKey"
          :tencent-enabled="tencentEnabled"
          :tencent-app-id="tencentAppId"
          :tencent-region="tencentRegion"
          :aliyun-enabled="aliyunEnabled"
          :aliyun-scene-id="aliyunSceneId"
          :aliyun-prefix="aliyunPrefix"
          :aliyun-region="aliyunRegion"
          @verify="onVerify"
          @expire="onExpire"
          @error="onError"
        />
      </div>

      <div
        v-else
        class="rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-200"
      >
        {{ t('checkin.errors.CHECKIN_CAPTCHA_NOT_CONFIGURED') }}
      </div>

      <p v-if="localError" class="text-xs text-red-600 dark:text-red-400">
        {{ localError }}
      </p>
    </div>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button
          type="button"
          class="btn btn-secondary btn-sm"
          :disabled="submitting || verifying"
          @click="handleClose"
        >
          {{ t('common.cancel') }}
        </button>
        <button
          type="button"
          data-testid="checkin-captcha-confirm"
          class="btn btn-primary btn-sm"
          :disabled="!canConfirm || submitting || verifying"
          @click="handleConfirm"
        >
          {{ submitting || verifying ? t('checkin.card.checkingIn') : t('checkin.captcha.confirm') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import CaptchaChallenge from '@/components/CaptchaChallenge.vue'
import { useAppStore } from '@/stores'
import type { ActionCaptchaRequestProof } from '@/types'

const props = withDefaults(
  defineProps<{
    show: boolean
    submitting?: boolean
  }>(),
  {
    submitting: false
  }
)

const emit = defineEmits<{
  close: []
  submit: [proof: ActionCaptchaRequestProof]
}>()

const { t } = useI18n()
const appStore = useAppStore()

const captchaRef = ref<InstanceType<typeof CaptchaChallenge> | null>(null)
const turnstileToken = ref('')
const localError = ref('')
const verifying = ref(false)

const settings = computed(() => appStore.cachedPublicSettings)
const turnstileEnabled = computed(() => Boolean(settings.value?.turnstile_enabled))
const turnstileSiteKey = computed(() => settings.value?.turnstile_site_key || '')
const tencentEnabled = computed(() => Boolean(settings.value?.tencent_captcha_enabled))
const tencentAppId = computed(() => settings.value?.tencent_captcha_app_id || '')
const tencentRegion = computed(() => settings.value?.tencent_captcha_region || 'cn')
const aliyunEnabled = computed(() => Boolean(settings.value?.aliyun_captcha_enabled))
const aliyunSceneId = computed(() => settings.value?.aliyun_captcha_scene_id || '')
const aliyunPrefix = computed(() => settings.value?.aliyun_captcha_prefix || '')
const aliyunRegion = computed(() => settings.value?.aliyun_captcha_region || 'cn')

const turnstileReady = computed(() => turnstileEnabled.value && Boolean(turnstileSiteKey.value))
const tencentReady = computed(() => tencentEnabled.value && Boolean(tencentAppId.value))
const aliyunReady = computed(
  () => aliyunEnabled.value && Boolean(aliyunSceneId.value) && Boolean(aliyunPrefix.value)
)
const hasAnyProvider = computed(
  () => turnstileReady.value || tencentReady.value || aliyunReady.value
)

const isActionCaptcha = computed(() => !turnstileReady.value && (tencentReady.value || aliyunReady.value))

const canConfirm = computed(() => {
  if (!hasAnyProvider.value) return false
  if (turnstileReady.value) return Boolean(turnstileToken.value)
  return isActionCaptcha.value
})

watch(
  () => props.show,
  (visible) => {
    if (visible) {
      turnstileToken.value = ''
      localError.value = ''
    }
  }
)

function onVerify(token: string) {
  turnstileToken.value = token
  localError.value = ''
}

function onExpire() {
  turnstileToken.value = ''
}

function onError() {
  turnstileToken.value = ''
  localError.value = t('checkin.errors.CAPTCHA_INVALID')
}

function reset() {
  turnstileToken.value = ''
  localError.value = ''
  captchaRef.value?.reset()
}

function handleClose() {
  reset()
  emit('close')
}

async function handleConfirm() {
  localError.value = ''
  if (turnstileReady.value) {
    if (!turnstileToken.value) {
      localError.value = t('checkin.errors.CAPTCHA_REQUIRED')
      return
    }
    emit('submit', { turnstile_token: turnstileToken.value })
    return
  }

  if (isActionCaptcha.value) {
    verifying.value = true
    try {
      const result = await captchaRef.value?.verifyAction()
      if (!result) {
        return
      }
      const proof: ActionCaptchaRequestProof = tencentReady.value
        ? {
            tencent_captcha_ticket: result.token,
            tencent_captcha_randstr: result.randstr
          }
        : {
            turnstile_token: result.token
          }
      emit('submit', proof)
    } finally {
      verifying.value = false
    }
  }
}

defineExpose({ reset })
</script>
