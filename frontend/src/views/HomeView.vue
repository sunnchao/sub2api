<template>
  <!-- Custom Home Content: Full Page Mode -->
  <div v-if="hasHomeContent" class="min-h-screen">
    <!-- iframe mode -->
    <iframe
      v-if="isHomeContentUrl"
      :src="homeContent.trim()"
      class="h-screen w-full border-0"
      allowfullscreen
    ></iframe>
    <!-- HTML mode - SECURITY: homeContent is admin-only setting, XSS risk is acceptable -->
    <div v-else v-html="homeContent"></div>
  </div>

  <!-- Compact Home Page -->
  <div
    v-else-if="compactHomeEnabled"
    data-testid="compact-home"
    class="flex min-h-screen flex-col bg-gray-50 text-gray-900 dark:bg-dark-950 dark:text-white"
  >
    <header class="border-b border-gray-200 px-4 py-4 sm:px-6 dark:border-dark-800">
      <nav class="mx-auto flex max-w-5xl flex-wrap items-center justify-between gap-3 sm:gap-4">
        <div class="flex min-w-0 flex-1 items-center gap-3">
          <img
            :src="siteLogo || '/logo.svg'"
            alt="Logo"
            class="h-9 w-9 shrink-0 rounded-lg object-contain"
          />
          <span class="min-w-0 truncate text-base font-semibold">{{ siteName }}</span>
        </div>
        <div class="flex max-w-full shrink-0 flex-wrap items-center justify-end gap-2">
          <LocaleSwitcher />
          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg text-gray-500 hover:bg-gray-100 dark:text-dark-400 dark:hover:bg-dark-800"
            :title="t('home.viewDocs')"
          >
            <Icon name="book" size="md" />
          </a>
          <router-link
            v-if="showModelPlazaEntry"
            to="/model-plaza"
            class="flex h-10 shrink-0 items-center gap-1.5 rounded-lg px-2.5 text-sm font-medium text-gray-500 hover:bg-gray-100 hover:text-gray-700 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-white"
            :title="t('nav.modelPlaza')"
          >
            <Icon name="grid" size="md" />
            <span class="hidden sm:inline">{{ t('nav.modelPlaza') }}</span>
          </router-link>
          <button
            class="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg text-gray-500 hover:bg-gray-100 dark:text-dark-400 dark:hover:bg-dark-800"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            @click="toggleTheme"
          >
            <Icon v-if="isDark" name="sun" size="md" />
            <Icon v-else name="moon" size="md" />
          </button>
          <router-link
            :to="isAuthenticated ? dashboardPath : '/login'"
            class="inline-flex min-h-10 shrink-0 items-center justify-center rounded-lg bg-gray-900 px-4 py-2 text-sm font-medium text-white hover:bg-gray-800 dark:bg-white dark:text-gray-900 dark:hover:bg-gray-200"
          >
            {{ isAuthenticated ? t('home.dashboard') : t('home.login') }}
          </router-link>
        </div>
      </nav>
    </header>

    <main class="flex min-w-0 flex-1 items-center justify-center px-4 py-16 sm:px-6">
      <div class="min-w-0 max-w-2xl text-center">
        <img
          :src="siteLogo || '/logo.svg'"
          alt="Logo"
          class="mx-auto mb-6 h-20 w-20 rounded-2xl object-contain"
        />
        <h1 class="[overflow-wrap:anywhere] text-3xl font-bold md:text-4xl">{{ siteName }}</h1>
        <p class="mt-4 whitespace-pre-wrap [overflow-wrap:anywhere] text-base text-gray-600 dark:text-dark-300">{{ siteSubtitle }}</p>
        <router-link
          :to="isAuthenticated ? dashboardPath : '/login'"
          class="mt-8 inline-flex min-h-10 items-center justify-center rounded-lg bg-primary-600 px-5 py-2.5 text-sm font-medium text-white hover:bg-primary-700"
        >
          {{ isAuthenticated ? t('home.goToDashboard') : t('home.login') }}
        </router-link>
      </div>
    </main>

    <footer class="min-w-0 border-t border-gray-200 px-4 py-5 text-center text-sm text-gray-500 [overflow-wrap:anywhere] sm:px-6 dark:border-dark-800 dark:text-dark-400">
      &copy; {{ currentYear }} {{ siteName }}
    </footer>
  </div>

  <!-- Default Home Page -->
  <div
    v-else
    class="relative flex min-h-screen flex-col overflow-x-hidden bg-white text-gray-900 dark:bg-dark-950 dark:text-white"
  >
    <!-- Backdrop -->
    <div class="pointer-events-none absolute inset-x-0 top-0 h-[760px] overflow-hidden" aria-hidden="true">
      <div class="absolute -left-40 -top-40 h-[460px] w-[460px] rounded-full bg-sky-300/30 blur-3xl dark:bg-sky-500/10"></div>
      <div class="absolute -right-32 top-0 h-[520px] w-[520px] rounded-full bg-violet-300/30 blur-3xl dark:bg-primary-500/15"></div>
      <div class="absolute left-1/3 top-48 h-72 w-72 rounded-full bg-primary-200/30 blur-3xl dark:bg-primary-700/10"></div>
      <div
        class="absolute inset-0 bg-[linear-gradient(rgba(99,102,241,0.05)_1px,transparent_1px),linear-gradient(90deg,rgba(99,102,241,0.05)_1px,transparent_1px)] bg-[size:56px_56px] [mask-image:linear-gradient(to_bottom,black,transparent_85%)]"
      ></div>
    </div>

    <!-- Header -->
    <header class="relative z-30 px-4 py-4 sm:px-6">
      <nav class="mx-auto flex max-w-6xl items-center justify-between gap-4">
        <router-link to="/home" class="flex min-w-0 items-center gap-2.5">
          <span class="h-9 w-9 shrink-0 overflow-hidden rounded-xl shadow-sm ring-1 ring-black/5 dark:ring-white/10">
            <img :src="siteLogo || '/logo.svg'" alt="Logo" class="h-full w-full object-contain" />
          </span>
          <span class="truncate text-base font-semibold tracking-tight">{{ siteName }}</span>
        </router-link>

        <div class="hidden items-center gap-1 md:flex">
          <router-link to="/home" :class="navLinkClass">{{ t('home.nav.home') }}</router-link>
          <router-link :to="isAuthenticated ? dashboardPath : '/login'" :class="navLinkClass">{{ t('home.dashboard') }}</router-link>
          <router-link v-if="showModelPlazaEntry" to="/model-plaza" :class="navLinkClass">{{ t('nav.modelPlaza') }}</router-link>
          <router-link to="/key-usage" :class="navLinkClass">{{ t('home.nav.keyUsage') }}</router-link>
          <a v-if="docUrl" :href="docUrl" target="_blank" rel="noopener noreferrer" :class="navLinkClass">{{ t('home.docs') }}</a>
        </div>

        <div class="flex items-center gap-2">
          <LocaleSwitcher />
          <button
            type="button"
            class="rounded-lg p-2 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-700 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-white"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            @click="toggleTheme"
          >
            <Icon v-if="isDark" name="sun" size="md" />
            <Icon v-else name="moon" size="md" />
          </button>
          <router-link
            v-if="isAuthenticated"
            :to="dashboardPath"
            class="inline-flex items-center gap-1.5 rounded-full bg-gray-900 py-1 pl-1 pr-3 transition-colors hover:bg-gray-800 dark:bg-gray-800 dark:hover:bg-gray-700"
          >
            <span class="flex h-6 w-6 items-center justify-center rounded-full bg-gradient-to-br from-primary-400 to-primary-600 text-[11px] font-semibold text-white">
              {{ userInitial }}
            </span>
            <span class="text-xs font-medium text-white">{{ t('home.dashboard') }}</span>
          </router-link>
          <router-link
            v-else
            to="/login"
            class="inline-flex items-center rounded-full bg-gray-900 px-4 py-1.5 text-xs font-medium text-white transition-colors hover:bg-gray-800 dark:bg-white dark:text-gray-900 dark:hover:bg-gray-200"
          >
            {{ t('home.login') }}
          </router-link>
          <button
            type="button"
            class="rounded-lg p-2 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-700 md:hidden dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-white"
            :aria-label="t('home.nav.menu')"
            :aria-expanded="menuOpen"
            @click="menuOpen = !menuOpen"
          >
            <Icon :name="menuOpen ? 'x' : 'menu'" size="md" />
          </button>
        </div>
      </nav>

      <!-- Mobile menu -->
      <div
        v-if="menuOpen"
        class="mx-auto mt-3 flex max-w-6xl flex-col gap-1 rounded-2xl border border-gray-200 bg-white/95 p-2 shadow-lg backdrop-blur md:hidden dark:border-dark-800 dark:bg-dark-900/95"
      >
        <router-link to="/home" :class="navLinkClass" @click="menuOpen = false">{{ t('home.nav.home') }}</router-link>
        <router-link :to="isAuthenticated ? dashboardPath : '/login'" :class="navLinkClass" @click="menuOpen = false">{{ t('home.dashboard') }}</router-link>
        <router-link v-if="showModelPlazaEntry" to="/model-plaza" :class="navLinkClass" @click="menuOpen = false">{{ t('nav.modelPlaza') }}</router-link>
        <router-link to="/key-usage" :class="navLinkClass" @click="menuOpen = false">{{ t('home.nav.keyUsage') }}</router-link>
        <a v-if="docUrl" :href="docUrl" target="_blank" rel="noopener noreferrer" :class="navLinkClass">{{ t('home.docs') }}</a>
      </div>
    </header>

    <main class="relative z-10 flex-1">
      <!-- Hero -->
      <section class="px-4 pb-16 pt-8 sm:px-6 lg:pb-24 lg:pt-14">
        <div class="mx-auto grid max-w-6xl grid-cols-1 items-center gap-12 lg:grid-cols-[minmax(0,1.05fr)_minmax(0,1fr)] lg:gap-16">
          <div class="min-w-0 text-center lg:text-left">
            <span
              class="reveal inline-flex items-center gap-2 rounded-full border border-primary-200/70 bg-primary-50/80 px-3 py-1 text-xs font-medium text-primary-700 dark:border-primary-500/30 dark:bg-primary-500/10 dark:text-primary-300"
            >
              <span class="h-1.5 w-1.5 rounded-full bg-primary-500"></span>
              {{ t('home.hero.badge') }}
            </span>
            <h1
              class="reveal mt-6 text-4xl font-bold leading-[1.15] tracking-tight sm:text-5xl"
              style="--d: 0.08s"
            >
              {{ t('home.hero.titleLead') }}
              <span class="gradient-text block">{{ t('home.hero.titleAccent') }}</span>
            </h1>
            <p
              class="reveal mx-auto mt-6 max-w-xl text-base leading-relaxed text-gray-600 sm:text-lg lg:mx-0 dark:text-dark-300"
              style="--d: 0.16s"
            >
              {{ t('home.hero.description') }}
            </p>

            <div
              class="reveal mt-8 flex flex-wrap items-center justify-center gap-3 lg:justify-start"
              style="--d: 0.24s"
            >
              <router-link :to="isAuthenticated ? dashboardPath : '/login'" class="btn btn-primary btn-lg">
                {{ isAuthenticated ? t('home.goToDashboard') : t('home.getStarted') }}
                <Icon name="arrowRight" size="sm" :stroke-width="2" />
              </router-link>
              <router-link :to="secondaryCta.to" class="btn btn-secondary btn-lg">
                {{ secondaryCta.label }}
              </router-link>
            </div>

            <div class="reveal mt-10" style="--d: 0.32s">
              <p class="text-xs font-semibold uppercase tracking-wider text-gray-400 dark:text-dark-500">
                {{ t('home.apps.title') }}
              </p>
              <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">{{ t('home.apps.description') }}</p>
              <ul class="mt-4 flex flex-wrap items-center justify-center gap-3 lg:justify-start">
                <li
                  v-for="app in apps"
                  :key="app.name"
                  class="inline-flex items-center gap-2 rounded-xl border border-gray-200 bg-white/80 px-3.5 py-2 text-sm font-medium text-gray-700 shadow-sm backdrop-blur dark:border-dark-700 dark:bg-dark-900/80 dark:text-dark-200"
                >
                  <Icon :name="app.icon" size="sm" class="text-primary-500" />
                  {{ app.name }}
                </li>
                <li class="px-2 text-sm text-gray-400 dark:text-dark-500">··· {{ t('home.apps.more') }}</li>
              </ul>
            </div>
          </div>

          <!-- Protocol demo -->
          <div class="terminal-container reveal relative mx-auto w-full min-w-0 max-w-xl lg:max-w-none" style="--d: 0.18s">
            <div class="demo-halo" aria-hidden="true"></div>
            <div
              class="relative overflow-hidden rounded-2xl border border-gray-200/80 bg-white/90 shadow-[0_30px_80px_-30px_rgba(79,70,229,0.35)] backdrop-blur-xl dark:border-dark-700 dark:bg-dark-900/90"
            >
              <div class="flex items-center justify-between gap-3 border-b border-gray-100 px-4 py-2.5 dark:border-dark-800">
                <div class="flex min-w-0 items-center gap-1 overflow-x-auto" role="tablist">
                  <button
                    v-for="(protocol, index) in protocols"
                    :key="protocol.id"
                    type="button"
                    role="tab"
                    :aria-selected="index === activeIndex"
                    :class="[
                      'shrink-0 rounded-md px-2.5 py-1 text-xs font-medium transition-colors',
                      index === activeIndex
                        ? 'bg-gray-900 text-white dark:bg-white dark:text-gray-900'
                        : 'text-gray-500 hover:bg-gray-100 hover:text-gray-800 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-white',
                    ]"
                    @click="selectProtocol(index)"
                  >
                    {{ protocol.label }}
                  </button>
                </div>
                <span class="inline-flex shrink-0 items-center gap-1.5 font-mono text-[11px] font-medium text-emerald-600 dark:text-emerald-400">
                  <span class="pulse-dot h-1.5 w-1.5 rounded-full bg-emerald-500"></span>
                  200 OK
                </span>
              </div>

              <div class="flex items-center gap-2 px-4 pt-3.5">
                <span class="rounded bg-primary-50 px-1.5 py-0.5 font-mono text-[10px] font-bold text-primary-700 dark:bg-primary-500/15 dark:text-primary-300">POST</span>
                <code class="truncate font-mono text-xs text-gray-700 dark:text-dark-200">{{ activeProtocol.path }}</code>
              </div>

              <div class="px-4 pb-3 pt-3">
                <p class="mb-1.5 text-[10px] font-semibold uppercase tracking-widest text-gray-400 dark:text-dark-500">
                  {{ t('home.demo.request') }}
                </p>
                <pre class="demo-code overflow-x-auto font-mono text-xs leading-relaxed text-gray-700 dark:text-dark-200"><code><span v-for="(line, i) in activeProtocol.request" :key="i" class="block">{{ line }}</span></code></pre>
              </div>

              <div class="border-t border-gray-100 px-4 pb-4 pt-3 dark:border-dark-800">
                <p class="mb-1.5 text-[10px] font-semibold uppercase tracking-widest text-gray-400 dark:text-dark-500">
                  {{ t('home.demo.response') }}
                </p>
                <pre class="demo-code overflow-x-auto font-mono text-xs leading-relaxed text-primary-700 dark:text-primary-300"><code><span v-for="(line, i) in activeProtocol.response" :key="i" class="block">{{ line }}</span></code></pre>
              </div>

              <div
                class="flex items-center justify-between gap-3 border-t border-gray-100 bg-gray-50/70 px-4 py-2.5 font-mono text-[10px] uppercase tracking-wider text-gray-400 dark:border-dark-800 dark:bg-dark-950/50 dark:text-dark-500"
              >
                <span>
                  {{ activeProtocol.latency }} ms · {{ activeProtocol.tokens }} {{ t('home.demo.tokens') }} ·
                  {{ t('home.demo.cost') }} ${{ activeProtocol.cost }}
                </span>
                <span class="hidden sm:inline">{{ t('home.demo.stream') }}</span>
              </div>
            </div>
          </div>
        </div>
      </section>

      <!-- Interactive AI Ecosystem Constellation -->
      <AiEcosystemConstellation />

      <!-- Core features -->
      <section class="px-4 pt-20 sm:px-6 sm:pt-24" v-if="false">
        <div class="mx-auto max-w-6xl">
          <div class="text-center">
            <p class="text-xs font-semibold uppercase tracking-widest text-primary-600 dark:text-primary-400">
              {{ t('home.features.eyebrow') }}
            </p>
            <h2 class="mt-3 text-3xl font-bold tracking-tight sm:text-4xl">{{ t('home.features.title') }}</h2>
          </div>

          <div class="mt-12 grid gap-5 md:grid-cols-2">
            <article
              v-for="item in featureHighlights"
              :key="item.titleKey"
              class="group rounded-2xl border border-gray-200 bg-white p-6 transition-all duration-300 hover:-translate-y-0.5 hover:border-primary-300 hover:shadow-xl hover:shadow-primary-500/10 dark:border-dark-800 dark:bg-dark-900 dark:hover:border-primary-500/40"
            >
              <div
                :class="['mb-5 flex h-11 w-11 items-center justify-center rounded-xl bg-gradient-to-br text-white shadow-lg transition-transform group-hover:scale-105', item.tone]"
              >
                <Icon :name="item.icon" size="md" />
              </div>
              <h3 class="text-lg font-semibold">{{ t(item.titleKey) }}</h3>
              <p class="mt-2 text-sm leading-relaxed text-gray-600 dark:text-dark-400">{{ t(item.descriptionKey) }}</p>
            </article>
          </div>

          <div class="mt-5 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <article
              v-for="item in featureExtras"
              :key="item.titleKey"
              class="rounded-xl border border-gray-200 bg-gray-50/70 p-5 dark:border-dark-800 dark:bg-dark-900/60"
            >
              <Icon :name="item.icon" size="md" class="text-primary-500" />
              <h3 class="mt-3 text-sm font-semibold">{{ t(item.titleKey) }}</h3>
              <p class="mt-1.5 text-xs leading-relaxed text-gray-500 dark:text-dark-400">{{ t(item.descriptionKey) }}</p>
            </article>
          </div>
        </div>
      </section>

      <!-- Workflow -->
      <section class="px-4 pt-24 sm:px-6">
        <div class="mx-auto max-w-6xl">
          <div class="text-center">
            <p class="text-xs font-semibold uppercase tracking-widest text-primary-600 dark:text-primary-400">
              {{ t('home.workflow.eyebrow') }}
            </p>
            <h2 class="mt-3 text-3xl font-bold tracking-tight sm:text-4xl">{{ t('home.workflow.title') }}</h2>
          </div>
          <ol class="mt-12 grid gap-5 md:grid-cols-3">
            <li
              v-for="(step, index) in steps"
              :key="step.titleKey"
              class="rounded-2xl border border-gray-200 bg-white p-6 dark:border-dark-800 dark:bg-dark-900"
            >
              <span class="gradient-text inline-block font-mono text-3xl font-bold">0{{ index + 1 }}</span>
              <h3 class="mt-4 text-lg font-semibold">{{ t(step.titleKey) }}</h3>
              <p class="mt-2 text-sm leading-relaxed text-gray-600 dark:text-dark-400">{{ t(step.descriptionKey) }}</p>
            </li>
          </ol>
        </div>
      </section>

      <!-- Final CTA -->
      <section class="px-4 pb-20 pt-24 sm:px-6">
        <div
          class="relative mx-auto max-w-6xl overflow-hidden rounded-3xl bg-gray-900 px-6 py-14 text-center sm:px-12 dark:border dark:border-dark-800 dark:bg-dark-900"
        >
          <div class="pointer-events-none absolute -right-24 -top-24 h-72 w-72 rounded-full bg-primary-500/30 blur-3xl" aria-hidden="true"></div>
          <div class="pointer-events-none absolute -bottom-24 -left-24 h-72 w-72 rounded-full bg-sky-500/20 blur-3xl" aria-hidden="true"></div>
          <h2 class="relative text-3xl font-bold tracking-tight text-white sm:text-4xl">{{ t('home.cta.title') }}</h2>
          <p class="relative mx-auto mt-4 max-w-xl text-sm leading-relaxed text-gray-300 sm:text-base">
            {{ t('home.cta.description') }}
          </p>
          <div class="relative mt-8 flex flex-wrap items-center justify-center gap-3">
            <router-link
              :to="isAuthenticated ? dashboardPath : '/login'"
              class="inline-flex items-center gap-2 rounded-lg bg-white px-6 py-3 text-base font-medium text-gray-900 transition-colors hover:bg-gray-100"
            >
              {{ isAuthenticated ? t('home.goToDashboard') : t('home.getStarted') }}
              <Icon name="arrowRight" size="sm" :stroke-width="2" />
            </router-link>
            <router-link
              :to="secondaryCta.to"
              class="inline-flex items-center rounded-lg border border-white/20 px-6 py-3 text-base font-medium text-white transition-colors hover:bg-white/10"
            >
              {{ secondaryCta.label }}
            </router-link>
          </div>
        </div>
      </section>
    </main>

    <!-- Footer -->
    <footer class="relative z-10 border-t border-gray-200 px-4 py-8 sm:px-6 dark:border-dark-800">
      <div class="mx-auto flex max-w-6xl flex-col items-center justify-between gap-4 text-center sm:flex-row sm:text-left">
        <p class="text-sm text-gray-500 dark:text-dark-400">
          &copy; {{ currentYear }} {{ siteName }}. {{ t('home.footer.allRightsReserved') }}
        </p>
        <div class="flex items-center gap-5">
          <router-link
            to="/key-usage"
            class="text-sm text-gray-500 transition-colors hover:text-gray-700 dark:text-dark-400 dark:hover:text-white"
          >
            {{ t('home.nav.keyUsage') }}
          </router-link>
          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="text-sm text-gray-500 transition-colors hover:text-gray-700 dark:text-dark-400 dark:hover:text-white"
          >
            {{ t('home.docs') }}
          </a>
<!--          <a-->
<!--            :href="githubUrl"-->
<!--            target="_blank"-->
<!--            rel="noopener noreferrer"-->
<!--            class="text-sm text-gray-500 transition-colors hover:text-gray-700 dark:text-dark-400 dark:hover:text-white"-->
<!--          >-->
<!--            GitHub-->
<!--          </a>-->
        </div>
      </div>
    </footer>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore, useAppStore } from '@/stores'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import Icon from '@/components/icons/Icon.vue'
import AiEcosystemConstellation from '@/components/home/AiEcosystemConstellation.vue'
import { sanitizeUrl } from '@/utils/url'
import { FeatureFlags, isFeatureFlagEnabled } from '@/utils/featureFlags'

const { t } = useI18n()

const authStore = useAuthStore()
const appStore = useAppStore()

// Site settings - directly from appStore (already initialized from injected config)
const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || 'Sub2API')
const siteLogo = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const siteSubtitle = computed(() => appStore.cachedPublicSettings?.site_subtitle || 'AI API Gateway Platform')
const docUrl = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.doc_url || appStore.docUrl || ''))
const homeContent = computed(() => appStore.cachedPublicSettings?.home_content || '')
const hasHomeContent = computed(() => homeContent.value.trim().length > 0)
const compactHomeEnabled = computed(() => appStore.cachedPublicSettings?.compact_home_enabled === true)
const modelPlazaEnabled = computed(() => isFeatureFlagEnabled(FeatureFlags.modelPlaza))

// Check if homeContent is a URL (for iframe display)
const isHomeContentUrl = computed(() => {
  const content = homeContent.value.trim()
  return content.startsWith('http://') || content.startsWith('https://')
})

// Theme
const isDark = ref(document.documentElement.classList.contains('dark'))

// GitHub URL
// const githubUrl = 'https://github.com/Wei-Shaw/sub2api'

// Auth state
const isAuthenticated = computed(() => authStore.isAuthenticated)
const modelPlazaRequiresAuth = computed(
  () => appStore.cachedPublicSettings?.model_plaza_require_auth === true,
)
const showModelPlazaEntry = computed(
  () => modelPlazaEnabled.value && (isAuthenticated.value || !modelPlazaRequiresAuth.value),
)
const isAdmin = computed(() => authStore.isAdmin)
const dashboardPath = computed(() => isAdmin.value ? '/admin/dashboard' : '/dashboard')
const userInitial = computed(() => {
  const user = authStore.user
  if (!user || !user.email) return ''
  return user.email.charAt(0).toUpperCase()
})

// Current year for footer
const currentYear = computed(() => new Date().getFullYear())

// Navigation
const menuOpen = ref(false)
const navLinkClass =
  'rounded-lg px-3 py-2 text-sm font-medium text-gray-600 transition-colors hover:bg-gray-100 hover:text-gray-900 dark:text-dark-300 dark:hover:bg-dark-800 dark:hover:text-white'
const secondaryCta = computed(() =>
  showModelPlazaEntry.value
    ? { to: '/model-plaza', label: t('home.hero.viewModels') }
    : { to: '/key-usage', label: t('home.hero.viewUsage') },
)

type HomeIcon =
  | 'terminal'
  | 'cube'
  | 'swap'
  | 'sync'
  | 'calculator'
  | 'key'
  | 'shield'
  | 'creditCard'
  | 'chartBar'
  | 'server'

// Clients that work by only changing the Base URL
const apps: { name: string; icon: HomeIcon }[] = [
  { name: 'Claude Code', icon: 'terminal' },
  { name: 'Codex CLI', icon: 'terminal' },
  { name: 'Gemini CLI', icon: 'terminal' },
  { name: 'OpenAI SDK', icon: 'cube' },
]

// Protocol demo: a trailing backslash marks a curl line continuation
const LINE_CONT = ' ' + String.fromCharCode(92)
interface DemoProtocol {
  id: string
  label: string
  path: string
  request: string[]
  response: string[]
  latency: number
  tokens: number
  cost: string
}
const protocols: DemoProtocol[] = [
  {
    id: 'chat',
    label: 'Chat',
    path: '/v1/chat/completions',
    request: [
      `curl -X POST "/v1/chat/completions"${LINE_CONT}`,
      `  -H "Authorization: Bearer sk-••••"${LINE_CONT}`,
      `  -d '{"model": "your-model",`,
      `       "messages": [{"role": "user", "content": "..."}]}'`,
    ],
    response: [
      '{',
      '  "choices": [{ "message": { "content": "Chat routed." } }],',
      '  "usage": { "prompt_tokens": 11, "completion_tokens": 18 }',
      '}',
    ],
    latency: 128,
    tokens: 29,
    cost: '0.00082',
  },
  {
    id: 'responses',
    label: 'Responses',
    path: '/v1/responses',
    request: [
      `curl -X POST "/v1/responses"${LINE_CONT}`,
      `  -H "Authorization: Bearer sk-••••"${LINE_CONT}`,
      `  -d '{"model": "your-model", "input": "..."}'`,
    ],
    response: [
      '{',
      '  "output": [{ "type": "message", "content": "Responses routed." }],',
      '  "usage": { "input_tokens": 11, "output_tokens": 18 }',
      '}',
    ],
    latency: 141,
    tokens: 29,
    cost: '0.00084',
  },
  {
    id: 'claude',
    label: 'Claude',
    path: '/v1/messages',
    request: [
      `curl -X POST "/v1/messages"${LINE_CONT}`,
      `  -H "x-api-key: sk-••••"${LINE_CONT}`,
      `  -H "anthropic-version: 2023-06-01"${LINE_CONT}`,
      `  -d '{"model": "your-model", "max_tokens": 1024,`,
      `       "messages": [{"role": "user", "content": "..."}]}'`,
    ],
    response: [
      '{',
      '  "content": [{ "type": "text", "text": "Claude message routed." }],',
      '  "usage": { "input_tokens": 11, "output_tokens": 18 }',
      '}',
    ],
    latency: 156,
    tokens: 29,
    cost: '0.00087',
  },
  {
    id: 'gemini',
    label: 'Gemini',
    path: '/v1beta/models/{model}:generateContent',
    request: [
      `curl -X POST "/v1beta/models/{model}:generateContent"${LINE_CONT}`,
      `  -H "x-goog-api-key: sk-••••"${LINE_CONT}`,
      `  -d '{"contents": [{"role": "user", "parts": [{"text": "..."}]}]}'`,
    ],
    response: [
      '{',
      '  "candidates": [{ "content": { "parts": [{ "text": "Gemini routed." }] } }],',
      '  "usageMetadata": { "totalTokenCount": 25 }',
      '}',
    ],
    latency: 93,
    tokens: 25,
    cost: '0.00075',
  },
]
const activeIndex = ref(0)
const activeProtocol = computed<DemoProtocol>(() => protocols[activeIndex.value] as DemoProtocol)
let rotateTimer: number | undefined

function stopRotation() {
  if (rotateTimer !== undefined) {
    window.clearInterval(rotateTimer)
    rotateTimer = undefined
  }
}

function startRotation() {
  if (window.matchMedia?.('(prefers-reduced-motion: reduce)')?.matches) return
  rotateTimer = window.setInterval(() => {
    activeIndex.value = (activeIndex.value + 1) % protocols.length
  }, 4500)
}

// A manual pick hands control to the visitor
function selectProtocol(index: number) {
  activeIndex.value = index
  stopRotation()
}

interface FeatureItem {
  icon: HomeIcon
  titleKey: string
  descriptionKey: string
  tone?: string
}
const featureHighlights: FeatureItem[] = [
  {
    icon: 'swap',
    titleKey: 'home.features.items.protocols.title',
    descriptionKey: 'home.features.items.protocols.desc',
    tone: 'from-sky-500 to-primary-500 shadow-sky-500/30',
  },
  {
    icon: 'sync',
    titleKey: 'home.features.items.scheduling.title',
    descriptionKey: 'home.features.items.scheduling.desc',
    tone: 'from-primary-500 to-violet-500 shadow-primary-500/30',
  },
  {
    icon: 'calculator',
    titleKey: 'home.features.items.billing.title',
    descriptionKey: 'home.features.items.billing.desc',
    tone: 'from-violet-500 to-fuchsia-500 shadow-violet-500/30',
  },
  {
    icon: 'key',
    titleKey: 'home.features.items.keys.title',
    descriptionKey: 'home.features.items.keys.desc',
    tone: 'from-emerald-500 to-teal-500 shadow-emerald-500/30',
  },
]
const featureExtras: FeatureItem[] = [
  { icon: 'shield', titleKey: 'home.features.items.limits.title', descriptionKey: 'home.features.items.limits.desc' },
  { icon: 'creditCard', titleKey: 'home.features.items.payment.title', descriptionKey: 'home.features.items.payment.desc' },
  { icon: 'chartBar', titleKey: 'home.features.items.admin.title', descriptionKey: 'home.features.items.admin.desc' },
  { icon: 'server', titleKey: 'home.features.items.selfHost.title', descriptionKey: 'home.features.items.selfHost.desc' },
]

const steps = [
  { titleKey: 'home.workflow.steps.configure.title', descriptionKey: 'home.workflow.steps.configure.desc' },
  { titleKey: 'home.workflow.steps.connect.title', descriptionKey: 'home.workflow.steps.connect.desc' },
  { titleKey: 'home.workflow.steps.monitor.title', descriptionKey: 'home.workflow.steps.monitor.desc' },
]

// Toggle theme
function toggleTheme() {
  isDark.value = !isDark.value
  document.documentElement.classList.toggle('dark', isDark.value)
  localStorage.setItem('theme', isDark.value ? 'dark' : 'light')
}

// Initialize theme
function initTheme() {
  const savedTheme = localStorage.getItem('theme')
  if (
    savedTheme === 'dark' ||
    (!savedTheme && window.matchMedia('(prefers-color-scheme: dark)').matches)
  ) {
    isDark.value = true
    document.documentElement.classList.add('dark')
  }
}

onMounted(() => {
  initTheme()

  // Check auth state
  authStore.checkAuth()

  // Ensure public settings are loaded (will use cache if already loaded from injected config)
  if (!appStore.publicSettingsLoaded) {
    appStore.fetchPublicSettings()
  }

  // Cycle through the protocol demo tabs until the visitor picks one
  startRotation()
})

onBeforeUnmount(stopRotation)
</script>

<style scoped>
/* Entrance animation */
.reveal {
  opacity: 0;
  transform: translateY(14px);
  animation: reveal-up 0.7s cubic-bezier(0.22, 1, 0.36, 1) forwards;
  animation-delay: var(--d, 0s);
}

@keyframes reveal-up {
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

/* Accent text */
.gradient-text {
  background-image: linear-gradient(100deg, #0ea5e9 0%, #6366f1 48%, #a855f7 100%);
  -webkit-background-clip: text;
  background-clip: text;
  color: transparent;
}

.dark .gradient-text {
  background-image: linear-gradient(100deg, #38bdf8 0%, #818cf8 48%, #c084fc 100%);
}

/* Protocol demo card */
.demo-halo {
  position: absolute;
  inset: -24px;
  border-radius: 32px;
  background:
    radial-gradient(60% 60% at 30% 20%, rgba(56, 189, 248, 0.35), transparent 70%),
    radial-gradient(60% 60% at 80% 80%, rgba(168, 85, 247, 0.3), transparent 70%);
  filter: blur(32px);
  opacity: 0.7;
}

.dark .demo-halo {
  opacity: 0.35;
}

.demo-code {
  scrollbar-width: thin;
  tab-size: 2;
}

.pulse-dot {
  animation: pulse-dot 2s ease-in-out infinite;
}

@keyframes pulse-dot {
  0%,
  100% {
    box-shadow: 0 0 0 0 rgba(16, 185, 129, 0.5);
  }
  50% {
    box-shadow: 0 0 0 5px rgba(16, 185, 129, 0);
  }
}

@media (prefers-reduced-motion: reduce) {
  .reveal {
    opacity: 1;
    transform: none;
    animation: none;
  }
  .pulse-dot {
    animation: none;
  }
}
</style>
