<template>
  <section
    ref="sectionRef"
    data-testid="ai-ecosystem-constellation"
    class="ecosystem-section relative overflow-hidden border-y border-stone-200/70 bg-gradient-to-b from-[#ffffff] via-[#f7f6f2] to-white px-4 py-20 sm:px-6 sm:py-24 dark:border-dark-800/80 dark:from-dark-950 dark:via-dark-900/90 dark:to-dark-950"
    :class="{ 'is-visible': isVisible }"
    @mousemove="handleStageMouseMove"
    @mouseleave="handleStageMouseLeave"
  >
    <!-- Subtle editorial ambient glow -->
    <div class="pointer-events-none absolute inset-0 overflow-hidden" aria-hidden="true">
      <div
        class="ecosystem-ambient-orb absolute left-1/2 top-1/2 h-[420px] w-[420px] -translate-x-1/2 -translate-y-1/2 rounded-full blur-3xl transition-colors duration-700"
        :style="{ background: activeAmbientGlow }"
      ></div>
    </div>

    <div class="relative z-10 mx-auto max-w-6xl">
      <!-- Top Title -->
      <div class="ecosystem-reveal text-center">
        <h2
          class="text-3xl font-bold tracking-tight text-gray-900 sm:text-4xl md:text-[42px] md:leading-tight dark:text-white"
        >
          {{ t('home.ecosystem.title') }}
        </h2>
      </div>

      <!-- Desktop Interactive Radial Constellation -->
      <div
        class="relative mx-auto mt-10 hidden aspect-[1000/580] w-full max-w-[1040px] select-none md:block"
      >
        <!-- SVG Connection Layer (Primary Dashed Spokes + Secondary Leaf Twigs + Travelling Pulses) -->
        <svg
          viewBox="0 0 1000 580"
          class="pointer-events-none absolute inset-0 h-full w-full overflow-visible"
          aria-hidden="true"
        >
          <defs>
            <filter id="ecosystem-glow" x="-50%" y="-50%" width="200%" height="200%">
              <feGaussianBlur stdDeviation="2.5" result="blur" />
              <feMerge>
                <feMergeNode in="blur" />
                <feMergeNode in="SourceGraphic" />
              </feMerge>
            </filter>
          </defs>

          <!-- Center ripple rings when hovering center orb -->
          <g v-if="centerHovered">
            <circle
              :cx="CENTER.x"
              :cy="CENTER.y"
              r="42"
              fill="none"
              stroke="currentColor"
              class="ecosystem-ripple text-primary-400/50"
              stroke-width="1.2"
            />
            <circle
              :cx="CENTER.x"
              :cy="CENTER.y"
              r="42"
              fill="none"
              stroke="currentColor"
              class="ecosystem-ripple ecosystem-ripple-delay text-violet-400/40"
              stroke-width="1"
            />
          </g>

          <!-- Branches: Spokes + Leaf Twigs -->
          <g v-for="(branch, idx) in branches" :key="branch.id">
            <!-- Secondary Leaf Twigs -->
            <line
              v-for="leaf in branch.leaves"
              :key="leaf.key"
              :x1="branch.x"
              :y1="branch.y"
              :x2="leaf.x"
              :y2="leaf.y"
              class="ecosystem-twig transition-all duration-500"
              :stroke="isBranchActive(branch.id) ? branch.accent : 'currentColor'"
              :stroke-opacity="isBranchActive(branch.id) ? 0.55 : 0.24"
              :stroke-width="isBranchActive(branch.id) ? 1.25 : 0.9"
              :style="{ transitionDelay: `${idx * 40}ms` }"
            />

            <!-- Primary Dashed Spoke -->
            <line
              :x1="CENTER.x"
              :y1="CENTER.y"
              :x2="branch.x"
              :y2="branch.y"
              class="ecosystem-spoke transition-all duration-500"
              :class="{ 'ecosystem-spoke-active': isBranchActive(branch.id) }"
              :stroke="isBranchActive(branch.id) ? branch.accent : 'currentColor'"
              :stroke-opacity="isBranchActive(branch.id) ? 0.72 : 0.26"
              :stroke-width="isBranchActive(branch.id) ? 1.5 : 1.1"
              stroke-dasharray="2.5 5.5"
              stroke-linecap="round"
            />

            <!-- Luminous Signal Pulse gliding along active spoke -->
            <circle
              v-if="isBranchActive(branch.id) && !reducedMotion"
              r="3.2"
              :fill="branch.accent"
              filter="url(#ecosystem-glow)"
              class="ecosystem-pulse-dot"
              :cx="pulsePosition(branch).x"
              :cy="pulsePosition(branch).y"
            />
          </g>
        </svg>

        <!-- Central 3D Particle Orb + Label -->
        <div
          class="absolute z-20 flex flex-col items-center"
          :style="{ left: `${CENTER.x / 10}%`, top: `${CENTER.y / 5.8}%`, transform: 'translate(-50%, -32%)' }"
        >
          <button
            type="button"
            data-testid="ecosystem-center-orb"
            class="group relative flex h-24 w-24 cursor-pointer items-center justify-center rounded-full focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500/60"
            :aria-label="t('home.ecosystem.centerTitle')"
            @mouseenter="centerHovered = true"
            @mouseleave="centerHovered = false"
            @click="cycleNextBranch"
          >
            <span
              class="pointer-events-none absolute inset-2 rounded-full opacity-0 blur-xl transition-opacity duration-500 group-hover:opacity-70"
              :style="{ background: activeBranch.accent }"
            ></span>
            <canvas
              ref="desktopCanvasRef"
              class="relative h-[88px] w-[88px] transition-transform duration-500 group-hover:scale-110"
              aria-hidden="true"
            />
          </button>
          <p
            class="mt-1 whitespace-nowrap text-xl font-semibold tracking-tight text-gray-900 dark:text-white"
          >
            {{ t('home.ecosystem.centerTitle') }}
          </p>
        </div>

        <!-- Five Main Branch Pills -->
        <button
          v-for="(branch, idx) in branches"
          :key="branch.id"
          type="button"
          :data-testid="`ecosystem-branch-${branch.id}`"
          :aria-pressed="activeBranchId === branch.id"
          class="ecosystem-pill absolute z-20 inline-flex cursor-pointer items-center gap-1.5 whitespace-nowrap rounded-full border px-4 py-1.5 text-sm font-medium transition-all duration-300 focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500/60"
          :class="
            isBranchActive(branch.id)
              ? 'border-transparent bg-white text-gray-950 shadow-lg dark:bg-dark-800 dark:text-white'
              : 'border-stone-200/80 bg-white/95 text-gray-800 shadow-sm hover:border-stone-300 hover:shadow-md dark:border-dark-700 dark:bg-dark-900/95 dark:text-gray-200 dark:hover:border-dark-600'
          "
          :style="branchPillStyle(branch, idx)"
          @mouseenter="selectBranch(branch.id, true)"
          @mouseleave="releaseHover"
          @focus="selectBranch(branch.id, true)"
          @blur="releaseHover"
          @click="selectBranch(branch.id, false)"
        >
          <span
            class="h-1.5 w-1.5 rounded-full transition-all duration-300"
            :style="{
              backgroundColor: branch.accent,
              transform: isBranchActive(branch.id) ? 'scale(1.3)' : 'scale(0.85)',
              opacity: isBranchActive(branch.id) ? 1 : 0.45,
            }"
          ></span>
          <span>{{ t(branch.titleKey) }}</span>
        </button>

        <!-- Leaf Nodes -->
        <template v-for="branch in branches" :key="`${branch.id}-leaves`">
          <button
            v-for="leaf in branch.leaves"
            :key="leaf.key"
            type="button"
            class="ecosystem-leaf absolute z-10 cursor-pointer whitespace-nowrap rounded-full bg-[#f8f7f3]/90 px-2 py-0.5 text-xs transition-all duration-300 focus:outline-none dark:bg-dark-950/85"
            :class="
              isBranchActive(branch.id)
                ? 'font-medium text-gray-900 dark:text-white'
                : 'text-gray-500 hover:text-gray-800 dark:text-dark-400 dark:hover:text-gray-200'
            "
            :style="{
              left: `${leaf.x / 10}%`,
              top: `${leaf.y / 5.8}%`,
              transform: isBranchActive(branch.id)
                ? 'translate(-50%, -50%) scale(1.05)'
                : 'translate(-50%, -50%)',
            }"
            @mouseenter="selectBranch(branch.id, true)"
            @mouseleave="releaseHover"
            @click="selectBranch(branch.id, false)"
          >
            {{ t(leaf.labelKey) }}
          </button>
        </template>
      </div>

      <!-- Mobile / Tablet Compact Interactive View -->
      <div class="mt-10 md:hidden">
        <div class="flex flex-col items-center">
          <button
            type="button"
            class="relative flex h-24 w-24 items-center justify-center rounded-full focus:outline-none"
            :aria-label="t('home.ecosystem.centerTitle')"
            @click="cycleNextBranch"
          >
            <canvas ref="mobileCanvasRef" class="h-[88px] w-[88px]" aria-hidden="true" />
          </button>
          <p class="mt-1 text-lg font-semibold text-gray-900 dark:text-white">
            {{ t('home.ecosystem.centerTitle') }}
          </p>
        </div>

        <div class="mt-6 grid grid-cols-1 gap-3 sm:grid-cols-2">
          <button
            v-for="branch in branches"
            :key="branch.id"
            type="button"
            class="rounded-2xl border p-4 text-left transition-all duration-300"
            :class="
              isBranchActive(branch.id)
                ? 'border-transparent bg-white shadow-md dark:bg-dark-800'
                : 'border-stone-200/80 bg-white/80 dark:border-dark-800 dark:bg-dark-900/80'
            "
            :style="
              isBranchActive(branch.id)
                ? { boxShadow: `0 0 0 1.5px ${branch.accent}55, 0 12px 24px -10px ${branch.accent}35` }
                : undefined
            "
            @click="selectBranch(branch.id, false)"
          >
            <div class="flex items-center gap-2">
              <span class="h-2 w-2 rounded-full" :style="{ backgroundColor: branch.accent }"></span>
              <span class="text-sm font-semibold text-gray-900 dark:text-white">
                {{ t(branch.titleKey) }}
              </span>
            </div>
            <div class="mt-2.5 flex flex-wrap gap-1.5">
              <span
                v-for="leaf in branch.leaves"
                :key="leaf.key"
                class="rounded-full bg-stone-100 px-2.5 py-0.5 text-xs text-gray-600 dark:bg-dark-700/80 dark:text-dark-300"
              >
                {{ t(leaf.labelKey) }}
              </span>
            </div>
          </button>
        </div>
      </div>

      <!-- Bottom Tagline & Subtle Footer Note -->
      <div class="mt-10 text-center md:mt-4">
        <p class="inline-flex items-center gap-2 text-sm text-gray-600 dark:text-dark-300">
          <span
            class="inline-block h-1.5 w-1.5 rounded-full transition-colors duration-500"
            :style="{ backgroundColor: activeBranch.accent }"
          ></span>
          <span>{{ t('home.ecosystem.tagline') }}</span>
        </p>
        <p class="mt-7 text-xs tracking-wide text-gray-400 dark:text-dark-500">
          {{ t('home.ecosystem.footerHint') }}
        </p>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

interface LeafNode {
  key: string
  labelKey: string
  x: number
  y: number
}

interface BranchNode {
  id: string
  titleKey: string
  accent: string
  x: number
  y: number
  leaves: LeafNode[]
}

interface SphereDot {
  x: number
  y: number
  z: number
  color: string
  accentGroup: string | null
  size: number
}

const { t } = useI18n()

const CENTER = { x: 500, y: 285 }

const branches: BranchNode[] = [
  {
    id: 'models',
    titleKey: 'home.ecosystem.branches.models.title',
    accent: '#6366f1',
    x: 586,
    y: 134,
    leaves: [
      { key: 'openai', labelKey: 'home.ecosystem.branches.models.leaf1', x: 544, y: 48 },
      { key: 'claude', labelKey: 'home.ecosystem.branches.models.leaf2', x: 604, y: 34 },
      { key: 'gemini', labelKey: 'home.ecosystem.branches.models.leaf3', x: 652, y: 62 },
      { key: 'deepseek', labelKey: 'home.ecosystem.branches.models.leaf4', x: 702, y: 92 },
    ],
  },
  {
    id: 'multimodal',
    titleKey: 'home.ecosystem.branches.multimodal.title',
    accent: '#f43f5e',
    x: 786,
    y: 285,
    leaves: [
      { key: 'text', labelKey: 'home.ecosystem.branches.multimodal.leaf1', x: 874, y: 222 },
      { key: 'vision', labelKey: 'home.ecosystem.branches.multimodal.leaf2', x: 916, y: 268 },
      { key: 'image', labelKey: 'home.ecosystem.branches.multimodal.leaf3', x: 906, y: 318 },
      { key: 'video', labelKey: 'home.ecosystem.branches.multimodal.leaf4', x: 876, y: 368 },
    ],
  },
  {
    id: 'usage',
    titleKey: 'home.ecosystem.branches.usage.title',
    accent: '#10b981',
    x: 716,
    y: 454,
    leaves: [
      { key: 'logs', labelKey: 'home.ecosystem.branches.usage.leaf1', x: 830, y: 454 },
      { key: 'tokens', labelKey: 'home.ecosystem.branches.usage.leaf2', x: 816, y: 514 },
      { key: 'cost', labelKey: 'home.ecosystem.branches.usage.leaf3', x: 722, y: 538 },
    ],
  },
  {
    id: 'production',
    titleKey: 'home.ecosystem.branches.production.title',
    accent: '#f59e0b',
    x: 280,
    y: 446,
    leaves: [
      { key: 'failover', labelKey: 'home.ecosystem.branches.production.leaf1', x: 174, y: 422 },
      { key: 'scheduling', labelKey: 'home.ecosystem.branches.production.leaf2', x: 176, y: 492 },
      { key: 'routing', labelKey: 'home.ecosystem.branches.production.leaf3', x: 252, y: 528 },
    ],
  },
  {
    id: 'integration',
    titleKey: 'home.ecosystem.branches.integration.title',
    accent: '#0ea5e9',
    x: 278,
    y: 248,
    leaves: [
      { key: 'apikey', labelKey: 'home.ecosystem.branches.integration.leaf1', x: 204, y: 172 },
      { key: 'sdk', labelKey: 'home.ecosystem.branches.integration.leaf2', x: 156, y: 224 },
      { key: 'compat', labelKey: 'home.ecosystem.branches.integration.leaf3', x: 188, y: 294 },
    ],
  },
]

const sectionRef = ref<HTMLElement | null>(null)
const desktopCanvasRef = ref<HTMLCanvasElement | null>(null)
const mobileCanvasRef = ref<HTMLCanvasElement | null>(null)

const isVisible = ref(true)
const centerHovered = ref(false)
const hoveredBranchId = ref<string | null>(null)
const activeBranchId = ref<string>('models')
const pulseProgress = ref(0)
const reducedMotion = ref(false)

// Pointer tilt target for 3D particle sphere
let targetTiltX = 0.28
let targetTiltY = 0
let currentTiltX = 0.28
let currentTiltY = 0
let rotY = 0
let rafId: number | null = null
let spotlightTimer: number | null = null
let observer: IntersectionObserver | null = null
let inViewport = true

const activeBranch = computed<BranchNode>(
  () => branches.find((b) => b.id === activeBranchId.value) ?? branches[0]!,
)

const activeAmbientGlow = computed(
  () => `radial-gradient(circle, ${activeBranch.value.accent}1a 0%, transparent 68%)`,
)

function isBranchActive(id: string): boolean {
  return centerHovered.value || activeBranchId.value === id
}

function selectBranch(id: string, isHover: boolean) {
  activeBranchId.value = id
  if (isHover) {
    hoveredBranchId.value = id
  }
  const branch = branches.find((b) => b.id === id)
  if (branch) {
    targetTiltY = ((branch.x - CENTER.x) / 500) * 0.6
    targetTiltX = 0.28 + ((branch.y - CENTER.y) / 300) * 0.4
  }
}

function releaseHover() {
  hoveredBranchId.value = null
}

function cycleNextBranch() {
  const idx = branches.findIndex((b) => b.id === activeBranchId.value)
  const next = branches[(idx + 1) % branches.length]!
  selectBranch(next.id, false)
}

function handleStageMouseMove(e: MouseEvent) {
  if (!sectionRef.value || reducedMotion.value) return
  const rect = sectionRef.value.getBoundingClientRect()
  if (rect.width <= 0 || rect.height <= 0) return
  const nx = (e.clientX - rect.left) / rect.width - 0.5
  const ny = (e.clientY - rect.top) / rect.height - 0.5
  targetTiltY = nx * 0.85
  targetTiltX = 0.28 + ny * 0.65
}

function handleStageMouseLeave() {
  hoveredBranchId.value = null
  targetTiltX = 0.28
  targetTiltY = 0
}

function pulsePosition(branch: BranchNode): { x: number; y: number } {
  const tVal = pulseProgress.value
  return {
    x: CENTER.x + (branch.x - CENTER.x) * tVal,
    y: CENTER.y + (branch.y - CENTER.y) * tVal,
  }
}

function branchPillStyle(branch: BranchNode, idx: number): Record<string, string> {
  const active = isBranchActive(branch.id)
  return {
    left: `${branch.x / 10}%`,
    top: `${branch.y / 5.8}%`,
    transform: active ? 'translate(-50%, -50%) scale(1.06)' : 'translate(-50%, -50%)',
    transitionDelay: `${idx * 25}ms`,
    ...(active
      ? {
          boxShadow: `0 0 0 1.5px ${branch.accent}55, 0 12px 28px -8px ${branch.accent}40`,
        }
      : {}),
  }
}

// Generate deterministic Fibonacci sphere point cloud with neutral charcoal/silver core + brand-accent dots
function buildSphereDots(count = 156): SphereDot[] {
  const dots: SphereDot[] = []
  const goldenAngle = Math.PI * (3 - Math.sqrt(5))
  const accentPool: Array<{ color: string; group: string }> = [
    { color: '#6366f1', group: 'models' },
    { color: '#f43f5e', group: 'multimodal' },
    { color: '#10b981', group: 'usage' },
    { color: '#f59e0b', group: 'production' },
    { color: '#0ea5e9', group: 'integration' },
  ]
  for (let i = 0; i < count; i++) {
    const y = 1 - (i / (count - 1)) * 2
    const radiusAtY = Math.sqrt(Math.max(0, 1 - y * y))
    const theta = goldenAngle * i
    const x = Math.cos(theta) * radiusAtY
    const z = Math.sin(theta) * radiusAtY
    const isAccent = i % 5 === 0
    const accentEntry = isAccent ? accentPool[(i / 5) % accentPool.length] : null
    dots.push({
      x,
      y,
      z,
      color: accentEntry ? accentEntry.color : '',
      accentGroup: accentEntry ? accentEntry.group : null,
      size: isAccent ? 2.1 : 1.45 + (i % 3) * 0.22,
    })
  }
  return dots
}

const sphereDots = buildSphereDots(160)

function canRender2DCanvas(): boolean {
  return typeof navigator === 'undefined' || !/jsdom/i.test(navigator.userAgent)
}

function drawSphereOnCanvas(canvas: HTMLCanvasElement | null, isDark: boolean) {
  if (!canvas || !canRender2DCanvas()) return
  const ctx = canvas.getContext('2d')
  if (!ctx) return

  const cssSize = 88
  const dpr = Math.min(window.devicePixelRatio || 1, 2)
  const targetWidth = Math.round(cssSize * dpr)
  if (canvas.width !== targetWidth || canvas.height !== targetWidth) {
    canvas.width = targetWidth
    canvas.height = targetWidth
  }

  ctx.save()
  ctx.scale(dpr, dpr)
  ctx.clearRect(0, 0, cssSize, cssSize)

  const half = cssSize / 2
  const radius = 34
  const cosY = Math.cos(rotY + currentTiltY)
  const sinY = Math.sin(rotY + currentTiltY)
  const cosX = Math.cos(currentTiltX)
  const sinX = Math.sin(currentTiltX)

  const projected: Array<{
    px: number
    py: number
    pz: number
    size: number
    color: string
    alpha: number
  }> = []

  for (const dot of sphereDots) {
    // Rotate around Y then X
    const x1 = dot.x * cosY - dot.z * sinY
    const z1 = dot.x * sinY + dot.z * cosY
    const y2 = dot.y * cosX - z1 * sinX
    const z2 = dot.y * sinX + z1 * cosX

    const depth = (z2 + 1) / 2 // 0 (back) -> 1 (front)
    const scale = 0.68 + depth * 0.5
    const isMatchedAccent = dot.accentGroup && dot.accentGroup === activeBranchId.value
    const px = half + x1 * radius
    const py = half + y2 * radius
    const alpha = 0.14 + depth * 0.82
    const baseColor = dot.color || (isDark ? '#cbd5e1' : '#334155')

    projected.push({
      px,
      py,
      pz: z2,
      size: dot.size * scale * (isMatchedAccent ? 1.28 : 1),
      color: baseColor,
      alpha: isMatchedAccent ? Math.min(1, alpha + 0.18) : alpha,
    })
  }

  projected.sort((a, b) => a.pz - b.pz)

  for (const p of projected) {
    ctx.globalAlpha = p.alpha
    ctx.fillStyle = p.color
    ctx.beginPath()
    ctx.arc(p.px, p.py, p.size, 0, Math.PI * 2)
    ctx.fill()
  }

  ctx.restore()
}

function renderFrame(timestamp: number) {
  if (!inViewport) {
    rafId = null
    return
  }
  const speed = centerHovered.value ? 0.014 : 0.0065
  rotY += speed
  currentTiltX += (targetTiltX - currentTiltX) * 0.08
  currentTiltY += (targetTiltY - currentTiltY) * 0.08
  pulseProgress.value = (timestamp % 2000) / 2000

  const isDark = document.documentElement.classList.contains('dark')
  drawSphereOnCanvas(desktopCanvasRef.value, isDark)
  drawSphereOnCanvas(mobileCanvasRef.value, isDark)

  rafId = window.requestAnimationFrame(renderFrame)
}

function startAnimation() {
  if (reducedMotion.value) {
    const isDark = document.documentElement.classList.contains('dark')
    drawSphereOnCanvas(desktopCanvasRef.value, isDark)
    drawSphereOnCanvas(mobileCanvasRef.value, isDark)
    return
  }
  if (rafId === null) {
    rafId = window.requestAnimationFrame(renderFrame)
  }
}

function stopAnimation() {
  if (rafId !== null) {
    window.cancelAnimationFrame(rafId)
    rafId = null
  }
}

function startSpotlightCycle() {
  if (reducedMotion.value || spotlightTimer !== null) return
  spotlightTimer = window.setInterval(() => {
    if (hoveredBranchId.value || centerHovered.value || !inViewport) return
    cycleNextBranch()
  }, 3400)
}

function stopSpotlightCycle() {
  if (spotlightTimer !== null) {
    window.clearInterval(spotlightTimer)
    spotlightTimer = null
  }
}

onMounted(() => {
  reducedMotion.value = Boolean(window.matchMedia?.('(prefers-reduced-motion: reduce)')?.matches)

  if (typeof IntersectionObserver !== 'undefined' && sectionRef.value) {
    observer = new IntersectionObserver(
      (entries) => {
        const entry = entries[0]
        if (!entry) return
        inViewport = entry.isIntersecting
        if (entry.isIntersecting) {
          isVisible.value = true
          startAnimation()
        } else {
          stopAnimation()
        }
      },
      { threshold: 0.1 },
    )
    observer.observe(sectionRef.value)
  }

  startAnimation()
  startSpotlightCycle()
})

onBeforeUnmount(() => {
  stopAnimation()
  stopSpotlightCycle()
  if (observer) {
    observer.disconnect()
    observer = null
  }
})
</script>

<style scoped>
.ecosystem-spoke {
  color: #9ca3af;
}

.dark .ecosystem-spoke {
  color: #475569;
}

.ecosystem-twig {
  color: #9ca3af;
}

.dark .ecosystem-twig {
  color: #475569;
}

.ecosystem-spoke-active {
  animation: spoke-flow 1.4s linear infinite;
}

@keyframes spoke-flow {
  from {
    stroke-dashoffset: 16;
  }
  to {
    stroke-dashoffset: 0;
  }
}

.ecosystem-ripple {
  transform-box: fill-box;
  transform-origin: center;
  animation: center-ripple 1.8s cubic-bezier(0.22, 1, 0.36, 1) infinite;
}

.ecosystem-ripple-delay {
  animation-delay: 0.9s;
}

@keyframes center-ripple {
  0% {
    opacity: 0.7;
    transform: scale(0.95);
  }
  100% {
    opacity: 0;
    transform: scale(2.15);
  }
}

.ecosystem-reveal {
  opacity: 0;
  transform: translateY(12px);
  transition:
    opacity 0.65s cubic-bezier(0.22, 1, 0.36, 1),
    transform 0.65s cubic-bezier(0.22, 1, 0.36, 1);
}

.is-visible .ecosystem-reveal {
  opacity: 1;
  transform: translateY(0);
}

@media (prefers-reduced-motion: reduce) {
  .ecosystem-spoke-active,
  .ecosystem-ripple {
    animation: none;
  }
  .ecosystem-reveal {
    opacity: 1;
    transform: none;
    transition: none;
  }
}
</style>
