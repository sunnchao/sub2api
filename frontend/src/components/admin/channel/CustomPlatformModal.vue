<template>
  <BaseDialog :show="show" :title="title" width="wide" @close="handleClose">
    <form id="custom-platform-form" class="space-y-6" @submit.prevent="handleSubmit">
      <div
        v-if="errorList.length"
        data-testid="custom-platform-errors"
        class="rounded-xl border border-red-300 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-800/60 dark:bg-red-900/20 dark:text-red-300"
      >
        <p class="font-medium">{{ t('admin.customPlatforms.errors.summary') }}</p>
        <ul class="mt-1 list-disc space-y-0.5 pl-5">
          <li v-for="key in errorList" :key="key">{{ t(key) }}</li>
        </ul>
      </div>
      <p
        v-if="formError"
        data-testid="custom-platform-form-error"
        class="rounded-xl border border-red-300 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-800/60 dark:bg-red-900/20 dark:text-red-300"
      >
        {{ formError }}
      </p>

      <!-- Basic info -->
      <section class="space-y-4">
        <h4 class="text-sm font-semibold text-gray-900 dark:text-white">
          {{ t('admin.customPlatforms.form.basicSection') }}
        </h4>
        <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
          <div>
            <label class="input-label" for="custom-platform-id">
              {{ t('admin.customPlatforms.form.id') }}
            </label>
            <input
              id="custom-platform-id"
              v-model="form.id"
              data-testid="custom-platform-id"
              type="text"
              class="input font-mono"
              :class="errors.id && 'input-error'"
              :disabled="!creating"
              autocomplete="off"
            />
            <p class="input-hint">
              {{
                creating
                  ? t('admin.customPlatforms.form.idHint')
                  : t('admin.customPlatforms.form.idImmutable')
              }}
            </p>
            <p v-if="errors.id" data-testid="custom-platform-error-id" class="input-error-text">
              {{ t(errors.id) }}
            </p>
          </div>

          <div>
            <label class="input-label" for="custom-platform-display-name">
              {{ t('admin.customPlatforms.form.displayName') }}
            </label>
            <input
              id="custom-platform-display-name"
              v-model="form.display_name"
              data-testid="custom-platform-display-name"
              type="text"
              class="input"
              :class="errors.display_name && 'input-error'"
              :placeholder="t('admin.customPlatforms.form.displayNamePlaceholder')"
              autocomplete="off"
            />
            <p
              v-if="errors.display_name"
              data-testid="custom-platform-error-display-name"
              class="input-error-text"
            >
              {{ t(errors.display_name) }}
            </p>
          </div>

          <div>
            <label class="input-label" for="custom-platform-sort-order">
              {{ t('admin.customPlatforms.form.sortOrder') }}
            </label>
            <input
              id="custom-platform-sort-order"
              v-model="form.sort_order"
              data-testid="custom-platform-sort-order"
              type="number"
              class="input"
            />
            <p class="input-hint">{{ t('admin.customPlatforms.form.sortOrderHint') }}</p>
          </div>

          <div>
            <label class="input-label">{{ t('admin.customPlatforms.form.enabled') }}</label>
            <div class="flex items-center gap-3">
              <Toggle v-model="form.enabled" data-testid="custom-platform-enabled" />
              <span class="text-sm text-gray-600 dark:text-gray-300">
                {{
                  form.enabled
                    ? t('admin.customPlatforms.status.enabled')
                    : t('admin.customPlatforms.status.disabled')
                }}
              </span>
            </div>
            <p class="input-hint">{{ t('admin.customPlatforms.form.enabledHint') }}</p>
          </div>
        </div>
      </section>

      <!-- Appearance -->
      <section class="space-y-4">
        <h4 class="text-sm font-semibold text-gray-900 dark:text-white">
          {{ t('admin.customPlatforms.form.appearanceSection') }}
        </h4>

        <div class="grid grid-cols-1 gap-5 lg:grid-cols-[minmax(0,1fr)_260px]">
          <div class="space-y-4">
            <div>
              <label class="input-label">{{ t('admin.customPlatforms.form.iconType') }}</label>
              <div class="flex flex-wrap gap-1 rounded-lg bg-gray-100 p-1 dark:bg-dark-700">
                <button
                  v-for="tab in ICON_TABS"
                  :key="tab"
                  type="button"
                  :data-testid="`icon-tab-${tab}`"
                  class="flex-1 rounded-md px-3 py-1.5 text-sm font-medium transition-colors"
                  :class="
                    form.icon_type === tab
                      ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-600 dark:text-white'
                      : 'text-gray-600 hover:text-gray-900 dark:text-dark-300 dark:hover:text-white'
                  "
                  @click="selectIconType(tab)"
                >
                  {{ t(`admin.customPlatforms.form.iconTabs.${tab}`) }}
                </button>
              </div>
            </div>

            <!-- Upload -->
            <div v-if="form.icon_type === 'data_url'" class="space-y-2">
              <input
                ref="iconFileInput"
                data-testid="custom-platform-icon-file"
                type="file"
                class="hidden"
                accept="image/png,image/jpeg,image/webp,image/svg+xml"
                @change="handleIconFile"
              />
              <div class="flex flex-wrap items-center gap-2">
                <button type="button" class="btn btn-secondary" @click="triggerIconUpload">
                  <Icon name="upload" size="sm" />
                  {{ t('admin.customPlatforms.form.iconUpload') }}
                </button>
                <button
                  v-if="form.icon_value"
                  type="button"
                  class="btn btn-secondary"
                  @click="form.icon_value = ''"
                >
                  <Icon name="x" size="sm" />
                  {{ t('admin.customPlatforms.form.iconRemove') }}
                </button>
              </div>
              <p class="input-hint">{{ t('admin.customPlatforms.form.iconUploadHint') }}</p>
            </div>

            <!-- URL -->
            <div v-else-if="form.icon_type === 'url'">
              <label class="input-label" for="custom-platform-icon-url">
                {{ t('admin.customPlatforms.form.iconUrl') }}
              </label>
              <input
                id="custom-platform-icon-url"
                v-model="form.icon_value"
                data-testid="custom-platform-icon-value"
                type="text"
                class="input"
                :class="errors.icon && 'input-error'"
                placeholder="https://cdn.example.com/logo.svg"
                autocomplete="off"
              />
              <p class="input-hint">{{ t('admin.customPlatforms.form.iconUrlHint') }}</p>
            </div>

            <!-- Builtin -->
            <div v-else-if="form.icon_type === 'builtin'">
              <label class="input-label" for="custom-platform-icon-builtin">
                {{ t('admin.customPlatforms.form.iconBuiltin') }}
              </label>
              <select
                id="custom-platform-icon-builtin"
                v-model="form.icon_value"
                data-testid="custom-platform-icon-builtin"
                class="input"
                :class="errors.icon && 'input-error'"
              >
                <option v-for="key in BUILTIN_PLATFORM_ICON_KEYS" :key="key" :value="key">
                  {{ key }}
                </option>
              </select>
            </div>

            <!-- Letter -->
            <div v-else>
              <label class="input-label" for="custom-platform-icon-letter">
                {{ t('admin.customPlatforms.form.iconLetter') }}
              </label>
              <input
                id="custom-platform-icon-letter"
                v-model="form.icon_value"
                data-testid="custom-platform-icon-value"
                type="text"
                maxlength="2"
                class="input w-32"
                :class="errors.icon && 'input-error'"
                autocomplete="off"
              />
              <p class="input-hint">{{ t('admin.customPlatforms.form.iconLetterHint') }}</p>
            </div>

            <p v-if="errors.icon" data-testid="custom-platform-error-icon" class="input-error-text">
              {{ t(errors.icon) }}
            </p>

            <div>
              <label class="input-label">{{ t('admin.customPlatforms.form.colorPreset') }}</label>
              <div class="flex flex-wrap gap-2">
                <button
                  v-for="preset in CUSTOM_PLATFORM_COLOR_PRESETS"
                  :key="preset"
                  type="button"
                  :data-testid="`color-preset-${preset}`"
                  :aria-pressed="form.color_preset === preset"
                  :title="preset"
                  :style="{ backgroundColor: presetAccentColor(preset) }"
                  class="flex h-8 w-8 items-center justify-center rounded-full ring-offset-2 transition-transform hover:scale-105 dark:ring-offset-dark-800"
                  :class="form.color_preset === preset ? 'ring-2 ring-gray-900 dark:ring-white' : ''"
                  @click="form.color_preset = preset"
                >
                  <Icon
                    v-if="form.color_preset === preset"
                    name="check"
                    size="sm"
                    class="text-white drop-shadow"
                  />
                </button>
              </div>
            </div>
          </div>

          <!-- Live preview -->
          <div
            data-testid="custom-platform-preview"
            class="rounded-2xl border border-gray-200 bg-gray-50 p-4 dark:border-dark-700 dark:bg-dark-900/40"
          >
            <p class="text-xs font-medium uppercase tracking-wide text-gray-500 dark:text-dark-400">
              {{ t('admin.customPlatforms.form.preview') }}
            </p>
            <div class="mt-3 flex items-center gap-3">
              <span
                class="flex h-11 w-11 flex-shrink-0 items-center justify-center rounded-xl border text-lg font-semibold"
                :class="presetBadgeClass(form.color_preset)"
              >
                <img
                  v-if="previewImage && !previewFailed"
                  :src="previewImage"
                  alt=""
                  referrerpolicy="no-referrer"
                  class="h-7 w-7 object-contain"
                  @error="previewFailed = true"
                />
                <svg
                  v-else-if="form.icon_type === 'builtin'"
                  class="h-6 w-6"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="1.6"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                >
                  <path :d="builtinIconPath" />
                </svg>
                <span v-else>{{ previewLetter }}</span>
              </span>
              <div class="min-w-0">
                <p class="truncate text-sm font-semibold" :class="presetTextClass(form.color_preset)">
                  {{ form.display_name || t('admin.customPlatforms.form.previewName') }}
                </p>
                <p class="truncate text-xs text-gray-500 dark:text-dark-400">
                  {{ form.id || 'custom_platform' }}
                </p>
              </div>
            </div>
            <div class="mt-4 flex flex-wrap gap-2">
              <span
                class="inline-flex items-center rounded-md border px-2 py-0.5 text-xs font-medium"
                :class="presetBadgeClass(form.color_preset)"
              >
                {{ t('admin.customPlatforms.form.previewText') }}
              </span>
            </div>
          </div>
        </div>
      </section>

      <!-- Endpoints -->
      <section class="space-y-4">
        <h4 class="text-sm font-semibold text-gray-900 dark:text-white">
          {{ t('admin.customPlatforms.form.endpointsSection') }}
        </h4>

        <div>
          <label class="input-label">{{ t('admin.customPlatforms.form.protocols') }}</label>
          <div class="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
            <label
              v-for="key in CUSTOM_PLATFORM_PROTOCOL_KEYS"
              :key="key"
              class="flex cursor-pointer items-center gap-2 rounded-lg border border-gray-200 px-3 py-2 text-sm text-gray-700 transition-colors hover:border-primary-300 dark:border-dark-600 dark:text-gray-200 dark:hover:border-primary-700"
            >
              <input
                v-model="form.protocols[key]"
                :data-testid="`protocol-${key}`"
                type="checkbox"
                class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500 dark:border-dark-500 dark:bg-dark-700"
              />
              <span>{{ t(`admin.customPlatforms.protocols.${key}`) }}</span>
            </label>
          </div>
          <p class="input-hint">{{ t('admin.customPlatforms.form.protocolsHint') }}</p>
          <p v-if="errors.protocols" data-testid="custom-platform-error-protocols" class="input-error-text">
            {{ t(errors.protocols) }}
          </p>
        </div>

        <div v-if="enabledProtocolKeys.length" class="grid grid-cols-1 gap-4 md:grid-cols-2">
          <div v-for="key in enabledProtocolKeys" :key="key">
            <label class="input-label" :for="`custom-platform-base-url-${key}`">
              {{ t(`admin.customPlatforms.protocols.${key}`) }}
            </label>
            <input
              :id="`custom-platform-base-url-${key}`"
              v-model="form.base_urls[key]"
              :data-testid="`base-url-${key}`"
              type="text"
              class="input"
              :class="errors[`base_urls.${key}`] && 'input-error'"
              :placeholder="t('admin.customPlatforms.form.baseUrlPlaceholder')"
              autocomplete="off"
            />
            <p
              v-if="errors[`base_urls.${key}`]"
              :data-testid="`custom-platform-error-base-url-${key}`"
              class="input-error-text"
            >
              {{ t(errors[`base_urls.${key}`]) }}
            </p>
          </div>
        </div>

        <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
          <div>
            <label class="input-label" for="custom-platform-responses-path">
              {{ t('admin.customPlatforms.form.responsesPath') }}
            </label>
            <input
              id="custom-platform-responses-path"
              v-model="form.responses_path"
              data-testid="responses-path"
              type="text"
              class="input font-mono"
              :class="errors.responses_path && 'input-error'"
              placeholder="/responses"
              autocomplete="off"
            />
            <p class="input-hint">{{ t('admin.customPlatforms.form.responsesPathHint') }}</p>
            <p
              v-if="errors.responses_path"
              data-testid="custom-platform-error-responses-path"
              class="input-error-text"
            >
              {{ t(errors.responses_path) }}
            </p>
          </div>

          <div>
            <label class="input-label" for="custom-platform-routing">
              {{ t('admin.customPlatforms.form.routing') }}
            </label>
            <select
              id="custom-platform-routing"
              v-model="form.routing"
              data-testid="routing"
              class="input"
            >
              <option value="by_inbound">
                {{ t('admin.customPlatforms.routing.by_inbound') }}
              </option>
              <option value="by_model">{{ t('admin.customPlatforms.routing.by_model') }}</option>
            </select>
            <p class="input-hint">{{ t('admin.customPlatforms.form.routingHint') }}</p>
          </div>

          <div>
            <label class="input-label" for="custom-platform-default-test-model">
              {{ t('admin.customPlatforms.form.defaultTestModel') }}
            </label>
            <input
              id="custom-platform-default-test-model"
              v-model="form.default_test_model"
              data-testid="default-test-model"
              type="text"
              class="input"
              :placeholder="t('admin.customPlatforms.form.defaultTestModelPlaceholder')"
              autocomplete="off"
            />
          </div>

          <div>
            <label class="input-label" for="custom-platform-litellm-provider">
              {{ t('admin.customPlatforms.form.litellmProvider') }}
            </label>
            <input
              id="custom-platform-litellm-provider"
              v-model="form.litellm_provider"
              data-testid="litellm-provider"
              type="text"
              class="input font-mono"
              list="custom-platform-litellm-providers"
              autocomplete="off"
            />
            <datalist id="custom-platform-litellm-providers">
              <option v-for="provider in litellmProviders" :key="provider" :value="provider" />
            </datalist>
            <p class="input-hint">{{ t('admin.customPlatforms.form.litellmProviderHint') }}</p>
          </div>
        </div>
      </section>

      <!-- Advanced -->
      <section class="rounded-xl border border-gray-200 dark:border-dark-700">
        <button
          type="button"
          data-testid="advanced-toggle"
          class="flex w-full items-center justify-between gap-3 px-4 py-3 text-left"
          @click="advancedOpen = !advancedOpen"
        >
          <span>
            <span class="block text-sm font-semibold text-gray-900 dark:text-white">
              {{ t('admin.customPlatforms.form.advancedSection') }}
            </span>
            <span class="mt-0.5 block text-xs text-gray-500 dark:text-dark-400">
              {{ t('admin.customPlatforms.form.advancedHint') }}
            </span>
          </span>
          <Icon
            name="chevronDown"
            size="md"
            class="flex-shrink-0 text-gray-400 transition-transform"
            :class="advancedOpen ? 'rotate-180' : ''"
          />
        </button>

        <div v-if="advancedOpen" class="space-y-6 border-t border-gray-200 px-4 py-4 dark:border-dark-700">
          <!-- Extra modes -->
          <div class="space-y-3">
            <div class="flex flex-wrap items-center justify-between gap-2">
              <div>
                <p class="text-sm font-medium text-gray-900 dark:text-white">
                  {{ t('admin.customPlatforms.form.extraModes') }}
                </p>
                <p class="text-xs text-gray-500 dark:text-dark-400">
                  {{ t('admin.customPlatforms.form.extraModesHint') }}
                </p>
              </div>
              <button
                type="button"
                data-testid="add-mode"
                class="btn btn-secondary btn-sm"
                @click="addExtraMode"
              >
                <Icon name="plus" size="sm" />
                {{ t('admin.customPlatforms.form.addMode') }}
              </button>
            </div>

            <p v-if="!form.extra_modes.length" class="text-xs text-gray-500 dark:text-dark-400">
              {{ t('admin.customPlatforms.form.noExtraModes') }}
            </p>

            <div
              v-for="(extra, index) in form.extra_modes"
              :key="index"
              class="space-y-3 rounded-xl border border-gray-200 p-3 dark:border-dark-700"
            >
              <div class="flex flex-wrap items-end gap-3">
                <div class="min-w-[140px] flex-1">
                  <label class="input-label" :for="`extra-mode-key-${index}`">
                    {{ t('admin.customPlatforms.form.modeKey') }}
                  </label>
                  <input
                    :id="`extra-mode-key-${index}`"
                    v-model="extra.mode"
                    :data-testid="`extra-mode-key-${index}`"
                    type="text"
                    class="input font-mono"
                    :class="errors[`extra_modes.${index}.mode`] && 'input-error'"
                    placeholder="cn"
                    autocomplete="off"
                  />
                </div>
                <div class="min-w-[160px] flex-1">
                  <label class="input-label" :for="`extra-mode-label-${index}`">
                    {{ t('admin.customPlatforms.form.modeLabel') }}
                  </label>
                  <input
                    :id="`extra-mode-label-${index}`"
                    v-model="extra.label"
                    :data-testid="`extra-mode-label-${index}`"
                    type="text"
                    class="input"
                    :placeholder="t('admin.customPlatforms.form.modeLabelPlaceholder')"
                    autocomplete="off"
                  />
                </div>
                <button
                  type="button"
                  :data-testid="`remove-mode-${index}`"
                  class="btn btn-secondary btn-sm mb-0.5"
                  @click="removeExtraMode(index)"
                >
                  <Icon name="trash" size="sm" />
                  {{ t('admin.customPlatforms.form.removeMode') }}
                </button>
              </div>
              <p
                v-if="errors[`extra_modes.${index}.mode`]"
                :data-testid="`custom-platform-error-extra-mode-${index}`"
                class="input-error-text"
              >
                {{ t(errors[`extra_modes.${index}.mode`]) }}
              </p>

              <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
                <div v-for="key in CUSTOM_PLATFORM_PROTOCOL_KEYS" :key="key">
                  <label class="input-label" :for="`extra-mode-${index}-${key}`">
                    {{ t(`admin.customPlatforms.protocols.${key}`) }}
                  </label>
                  <input
                    :id="`extra-mode-${index}-${key}`"
                    v-model="extra.base_urls[key]"
                    :data-testid="`extra-mode-base-url-${index}-${key}`"
                    type="text"
                    class="input"
                    :class="errors[`extra_modes.${index}.base_urls.${key}`] && 'input-error'"
                    :placeholder="t('admin.customPlatforms.form.baseUrlPlaceholder')"
                    autocomplete="off"
                  />
                  <p
                    v-if="errors[`extra_modes.${index}.base_urls.${key}`]"
                    class="input-error-text"
                  >
                    {{ t(errors[`extra_modes.${index}.base_urls.${key}`]) }}
                  </p>
                </div>
              </div>
              <p
                v-if="errors[`extra_modes.${index}.base_urls`]"
                :data-testid="`custom-platform-error-extra-mode-urls-${index}`"
                class="input-error-text"
              >
                {{ t(errors[`extra_modes.${index}.base_urls`]) }}
              </p>
            </div>
          </div>

          <!-- Protocol rules -->
          <div class="space-y-3">
            <div class="flex flex-wrap items-center justify-between gap-2">
              <div>
                <p class="text-sm font-medium text-gray-900 dark:text-white">
                  {{ t('admin.customPlatforms.form.protocolRules') }}
                </p>
                <p class="text-xs text-gray-500 dark:text-dark-400">
                  {{ t('admin.customPlatforms.form.protocolRulesHint') }}
                </p>
              </div>
              <button
                type="button"
                data-testid="add-rule"
                class="btn btn-secondary btn-sm"
                :disabled="!dialogueProtocols.length"
                @click="addProtocolRule"
              >
                <Icon name="plus" size="sm" />
                {{ t('admin.customPlatforms.form.addRule') }}
              </button>
            </div>

            <p v-if="!form.protocol_rules.length" class="text-xs text-gray-500 dark:text-dark-400">
              {{ t('admin.customPlatforms.form.noRules') }}
            </p>

            <div
              v-for="(rule, index) in form.protocol_rules"
              :key="index"
              class="flex flex-wrap items-end gap-3"
            >
              <div class="min-w-[180px] flex-1">
                <label class="input-label" :for="`rule-pattern-${index}`">
                  {{ t('admin.customPlatforms.form.rulePattern') }}
                </label>
                <input
                  :id="`rule-pattern-${index}`"
                  v-model="rule.pattern"
                  :data-testid="`rule-pattern-${index}`"
                  type="text"
                  class="input font-mono"
                  :class="errors[`protocol_rules.${index}.pattern`] && 'input-error'"
                  placeholder="claude-*"
                  autocomplete="off"
                />
              </div>
              <div class="w-48">
                <label class="input-label" :for="`rule-protocol-${index}`">
                  {{ t('admin.customPlatforms.form.ruleProtocol') }}
                </label>
                <select
                  :id="`rule-protocol-${index}`"
                  v-model="rule.protocol"
                  :data-testid="`rule-protocol-${index}`"
                  class="input"
                  :class="errors[`protocol_rules.${index}.protocol`] && 'input-error'"
                >
                  <option v-for="key in dialogueProtocols" :key="key" :value="key">
                    {{ t(`admin.customPlatforms.protocols.${key}`) }}
                  </option>
                </select>
              </div>
              <button
                type="button"
                :data-testid="`remove-rule-${index}`"
                class="btn btn-secondary btn-sm mb-0.5"
                @click="removeProtocolRule(index)"
              >
                <Icon name="trash" size="sm" />
                {{ t('admin.customPlatforms.form.removeRule') }}
              </button>
              <p
                v-if="errors[`protocol_rules.${index}.pattern`]"
                :data-testid="`custom-platform-error-rule-pattern-${index}`"
                class="w-full input-error-text"
              >
                {{ t(errors[`protocol_rules.${index}.pattern`]) }}
              </p>
              <p
                v-if="errors[`protocol_rules.${index}.protocol`]"
                :data-testid="`custom-platform-error-rule-protocol-${index}`"
                class="w-full input-error-text"
              >
                {{ t(errors[`protocol_rules.${index}.protocol`]) }}
              </p>
            </div>
          </div>

          <!-- Model catalog -->
          <div class="flex items-start gap-3">
            <Toggle v-model="form.model_catalog" data-testid="model-catalog" />
            <div>
              <p class="text-sm font-medium text-gray-900 dark:text-white">
                {{ t('admin.customPlatforms.form.modelCatalog') }}
              </p>
              <p class="text-xs text-gray-500 dark:text-dark-400">
                {{ t('admin.customPlatforms.form.modelCatalogHint') }}
              </p>
            </div>
          </div>
        </div>
      </section>
    </form>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary" @click="handleClose">
          {{ t('admin.customPlatforms.form.cancel') }}
        </button>
        <button
          type="submit"
          form="custom-platform-form"
          data-testid="save-platform"
          class="btn btn-primary"
          :disabled="saving || iconUploading"
        >
          {{ saving ? t('admin.customPlatforms.form.saving') : t('admin.customPlatforms.form.save') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Toggle from '@/components/common/Toggle.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import {
  create as createCustomPlatform,
  listLitellmProviders,
  update as updateCustomPlatform,
  type CustomPlatformAdmin
} from '@/api/admin/customPlatforms'
import type { PlatformIconType } from '@/constants/platformCatalog'
import { BUILTIN_PLATFORM_ICON_PATHS } from '@/constants/platformIcons'
import { sanitizeUrl } from '@/utils/url'
import {
  BUILTIN_PLATFORM_ICON_KEYS,
  CUSTOM_PLATFORM_COLOR_PRESETS,
  CUSTOM_PLATFORM_DIALOGUE_PROTOCOL_KEYS,
  CUSTOM_PLATFORM_MAX_ICON_BYTES,
  CUSTOM_PLATFORM_PROTOCOL_KEYS,
  buildCustomPlatformPayload,
  createEmptyCustomPlatformForm,
  formFromCustomPlatform,
  presetAccentColor,
  presetBadgeClass,
  presetTextClass,
  validateCustomPlatformForm,
  validateIconValue,
  validateImageDataUrl,
  type CustomPlatformFormErrors,
  type CustomPlatformFormState,
  type CustomPlatformProtocolKey
} from './customPlatformForm'

interface Props {
  show: boolean
  platform?: CustomPlatformAdmin | null
  /** 内置平台 ID，用于创建时的冲突校验。 */
  builtinIds?: string[]
  /** 已存在的自定义平台 ID，用于创建时的重复校验。 */
  customIds?: string[]
}

const props = withDefaults(defineProps<Props>(), {
  platform: null,
  builtinIds: () => [],
  customIds: () => []
})

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'saved', platform: CustomPlatformAdmin): void
}>()

const { t } = useI18n()

const ICON_TABS: PlatformIconType[] = ['data_url', 'url', 'builtin', 'letter']

const form = reactive<CustomPlatformFormState>(createEmptyCustomPlatformForm())
const errors = ref<CustomPlatformFormErrors>({})
const formError = ref('')
const saving = ref(false)
const iconUploading = ref(false)
const advancedOpen = ref(false)
const previewFailed = ref(false)
const litellmProviders = ref<string[]>([])
const providersLoaded = ref(false)
const iconFileInput = ref<HTMLInputElement | null>(null)

const creating = computed(() => !props.platform)
const existingCustomIds = computed(() =>
  creating.value ? props.customIds : props.customIds.filter(id => id !== props.platform?.id)
)
const title = computed(() =>
  creating.value
    ? t('admin.customPlatforms.form.createTitle')
    : t('admin.customPlatforms.form.editTitle')
)

const enabledProtocolKeys = computed<CustomPlatformProtocolKey[]>(() =>
  CUSTOM_PLATFORM_PROTOCOL_KEYS.filter(key => form.protocols[key])
)
const dialogueProtocols = computed<CustomPlatformProtocolKey[]>(() =>
  CUSTOM_PLATFORM_DIALOGUE_PROTOCOL_KEYS.filter(key => form.protocols[key])
)
const errorList = computed(() => [...new Set(Object.values(errors.value))])
const previewImage = computed(() => {
  if (form.icon_type !== 'url' && form.icon_type !== 'data_url') return ''
  if (validateIconValue(form.icon_type, form.icon_value) !== null) return ''
  return sanitizeUrl(form.icon_value, { allowRelative: true, allowDataUrl: true }) ?? ''
})
const previewLetter = computed(() => {
  const explicit = [...form.icon_value.trim()][0]
  const first = [...form.display_name.trim()][0]
  return explicit ?? first ?? 'A'
})
const builtinIconPath = computed(
  () => BUILTIN_PLATFORM_ICON_PATHS[form.icon_value as keyof typeof BUILTIN_PLATFORM_ICON_PATHS] ?? BUILTIN_PLATFORM_ICON_PATHS.cloud
)

function resetForm() {
  Object.assign(
    form,
    props.platform
      ? formFromCustomPlatform(props.platform)
      : createEmptyCustomPlatformForm()
  )
  errors.value = {}
  formError.value = ''
  previewFailed.value = false
  advancedOpen.value = Boolean(form.extra_modes.length || form.protocol_rules.length)
}

async function loadProviders() {
  if (providersLoaded.value) return
  providersLoaded.value = true
  try {
    litellmProviders.value = await listLitellmProviders()
  } catch {
    litellmProviders.value = []
  }
}

watch(
  () => [props.show, props.platform] as const,
  ([show]) => {
    if (!show) return
    resetForm()
    void loadProviders()
  },
  { immediate: true }
)

// 清掉已修正字段的错误，避免用户改完后仍看到红字。
watch(
  () => form,
  () => {
    if (Object.keys(errors.value).length) {
      const next = validateCustomPlatformForm(form, {
        creating: creating.value,
        builtinIds: props.builtinIds,
        customIds: existingCustomIds.value
      })
      errors.value = next
    }
    previewFailed.value = false
  },
  { deep: true }
)

function selectIconType(type: PlatformIconType) {
  form.icon_type = type
  if (type === 'builtin' && !(BUILTIN_PLATFORM_ICON_KEYS as readonly string[]).includes(form.icon_value)) {
    form.icon_value = BUILTIN_PLATFORM_ICON_KEYS[0]
  }
  if (type === 'letter') {
    form.icon_value = form.icon_value.trim().slice(0, 2)
  }
  previewFailed.value = false
}

function triggerIconUpload() {
  iconFileInput.value?.click()
}

function readFileAsDataUrl(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result ?? ''))
    reader.onerror = () => reject(reader.error ?? new Error('read failed'))
    reader.readAsDataURL(file)
  })
}

async function handleIconFile(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return

  const allowed = ['image/png', 'image/jpeg', 'image/webp', 'image/svg+xml']
  if (!allowed.includes(file.type)) {
    errors.value = { ...errors.value, icon: 'admin.customPlatforms.errors.fileTypeInvalid' }
    return
  }
  if (file.size > CUSTOM_PLATFORM_MAX_ICON_BYTES) {
    errors.value = { ...errors.value, icon: 'admin.customPlatforms.errors.fileTooLarge' }
    return
  }

  iconUploading.value = true
  try {
    const dataUrl = await readFileAsDataUrl(file)
    const problem = validateImageDataUrl(dataUrl)
    if (problem) {
      errors.value = { ...errors.value, icon: problem }
      return
    }
    form.icon_type = 'data_url'
    form.icon_value = dataUrl
    previewFailed.value = false
    const next = { ...errors.value }
    delete next.icon
    errors.value = next
  } catch {
    errors.value = { ...errors.value, icon: 'admin.customPlatforms.errors.iconDataInvalid' }
  } finally {
    iconUploading.value = false
  }
}

function addExtraMode() {
  form.extra_modes.push({
    mode: '',
    label: '',
    base_urls: {
      chat_completions: '',
      responses: '',
      anthropic: '',
      embeddings: '',
      images: ''
    },
    protocol_rules: []
  })
}

function removeExtraMode(index: number) {
  form.extra_modes.splice(index, 1)
}

function addProtocolRule() {
  const protocol = dialogueProtocols.value[0] ?? 'chat_completions'
  form.protocol_rules.push({ pattern: '', protocol })
}

function removeProtocolRule(index: number) {
  form.protocol_rules.splice(index, 1)
}

function handleClose() {
  if (saving.value) return
  emit('close')
}

async function handleSubmit() {
  const validation = validateCustomPlatformForm(form, {
    creating: creating.value,
    builtinIds: props.builtinIds,
    customIds: existingCustomIds.value
  })
  errors.value = validation
  formError.value = ''
  if (Object.keys(validation).length) return

  saving.value = true
  try {
    const payload = buildCustomPlatformPayload(form)
    const saved = creating.value
      ? await createCustomPlatform(payload)
      : await updateCustomPlatform(props.platform!.id, payload)
    emit('saved', saved)
    emit('close')
  } catch (error) {
    formError.value =
      (typeof error === 'object' && error !== null && 'message' in error
        ? String((error as { message?: unknown }).message ?? '')
        : '') || t('admin.customPlatforms.errors.saveFailed')
  } finally {
    saving.value = false
  }
}
</script>
