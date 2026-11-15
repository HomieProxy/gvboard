<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  ActivityIcon, ArrowRightIcon, GlobeIcon, LayersIcon, LogInIcon, MonitorSmartphoneIcon, MouseIcon, QrCodeIcon,
  ShieldCheckIcon, ShoppingBagIcon, UserPlusIcon, WaypointsIcon, ZapIcon,
} from '@lucide/vue'
import { cfg } from '../api'
import TopActions from '../components/TopActions.vue'
import { createGlobe, type GlobeStats } from '../home/globe'
import { vReveal } from '../home/reveal'

const { t } = useI18n()

const name = computed(() => cfg.value.site.name)
const desc = computed(() => cfg.value.site.description || t('home.tagline'))
const canRegister = computed(() => !cfg.value.register.closed)
const startTo = computed(() => (canRegister.value ? '/register' : '/login'))
const year = new Date().getFullYear()

const features = [
  { key: 'global', icon: GlobeIcon },
  { key: 'protocol', icon: WaypointsIcon },
  { key: 'client', icon: MonitorSmartphoneIcon },
  { key: 'multi', icon: LayersIcon },
  { key: 'traffic', icon: ActivityIcon },
  { key: 'secure', icon: ShieldCheckIcon },
] as const
const steps = [
  { key: 'register', icon: UserPlusIcon },
  { key: 'plan', icon: ShoppingBagIcon },
  { key: 'import', icon: QrCodeIcon },
] as const
const protocols = ['VLESS', 'REALITY', 'VMess', 'Trojan', 'Shadowsocks', 'Hysteria2', 'TUIC', 'AnyTLS']

const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches

// 地球
const canvas = ref<HTMLCanvasElement>()
const stats = ref<GlobeStats>({ lon: 105, lat: 18, links: 0 })
let globe: ReturnType<typeof createGlobe> | null = null
const coord = (v: number, pos: string, neg: string) => `${Math.abs(v).toFixed(1).padStart(5, '0')}°${v >= 0 ? pos : neg}`

// 标题“解码”入场：字符从随机符号逐个定格为站点名
const title = ref(name.value)
let scrambleTimer = 0
function scramble() {
  const target = name.value
  if (reduced || !target) {
    title.value = target
    return
  }
  const glyphs = '01<>/\\[]{}#$%&*+=?ABCDEFXYZ'
  const start = performance.now()
  const total = 900 + target.length * 60
  const tick = (now: number) => {
    const k = (now - start) / total
    const fixed = Math.floor(k * target.length)
    let s = ''
    for (let i = 0; i < target.length; i++) {
      const ch = target[i]
      s += i < fixed || ch === ' ' ? ch : glyphs[(Math.random() * glyphs.length) | 0]
    }
    title.value = s
    if (k < 1) scrambleTimer = requestAnimationFrame(tick)
    else title.value = target
  }
  scrambleTimer = requestAnimationFrame(tick)
}

// 特性卡片的鼠标跟随光斑
function spot(e: PointerEvent) {
  const el = e.currentTarget as HTMLElement
  const r = el.getBoundingClientRect()
  el.style.setProperty('--x', `${e.clientX - r.left}px`)
  el.style.setProperty('--y', `${e.clientY - r.top}px`)
}

onMounted(() => {
  if (canvas.value) globe = createGlobe(canvas.value, { reducedMotion: reduced, onStats: (s) => (stats.value = s) })
  scramble()
})
onBeforeUnmount(() => {
  globe?.destroy()
  cancelAnimationFrame(scrambleTimer)
})
</script>

<template>
  <div class="min-h-svh bg-background text-foreground">
    <!-- 首屏：固定深色 -->
    <section class="dark relative isolate flex min-h-svh flex-col overflow-hidden bg-[#030712] text-foreground">
      <div class="home-aurora pointer-events-none absolute inset-0 -z-10" />
      <div class="home-stars pointer-events-none absolute inset-0 -z-10" />
      <div class="home-grid pointer-events-none absolute inset-0 -z-10" />
      <div class="home-scan pointer-events-none absolute inset-x-0 top-0 -z-10" />

      <header class="pointer-events-none relative z-20 flex items-center justify-between gap-3 px-4 py-4 sm:px-8">
        <RouterLink to="/" class="pointer-events-auto flex min-w-0 items-center gap-2.5">
          <img v-if="cfg.site.logo" :src="cfg.site.logo" alt="" class="h-8 w-auto" />
          <span v-else class="home-logo grid size-8 place-items-center rounded-lg"><ZapIcon class="size-4 text-cyan-200" /></span>
          <span class="truncate text-base font-semibold tracking-tight text-slate-100">{{ name }}</span>
        </RouterLink>
        <div class="pointer-events-auto flex items-center gap-1 sm:gap-2">
          <TopActions class="rounded-md bg-slate-900/70 ring-1 ring-white/10" />
          <RouterLink
            to="/login"
            class="hidden h-8 items-center gap-1.5 rounded-md px-3 text-sm font-medium text-slate-200 transition hover:bg-white/10 sm:inline-flex"
          >
            <LogInIcon class="size-4" />{{ t('auth.login') }}
          </RouterLink>
          <RouterLink v-if="canRegister" to="/register" class="home-btn h-8 rounded-md px-3 text-sm">{{ t('auth.register') }}</RouterLink>
        </div>
      </header>

      <div class="relative z-10 mx-auto grid w-full max-w-7xl flex-1 content-center items-center gap-2 px-5 pb-16 sm:px-8 lg:grid-cols-2 lg:gap-8 lg:pb-8">
        <!-- 画布比容器大一圈，给卫星轨道留出空间；桌面端不受栏宽限制，按屏幕高度定尺寸 -->
        <div
          class="relative mx-auto aspect-square w-[min(80vw,340px)] lg:absolute lg:start-[72%] lg:top-1/2 lg:w-[min(60vw,74vh)] lg:-translate-x-1/2 lg:-translate-y-1/2 lg:rtl:translate-x-1/2"
        >
          <canvas ref="canvas" class="absolute -top-[40%] -left-[40%] size-[180%] touch-pan-y" />
        </div>
        <div class="relative z-10">
          <div class="mx-auto max-w-xl text-center lg:mx-0 lg:text-start">
            <span class="home-in inline-flex items-center gap-2 rounded-full border border-cyan-400/25 bg-cyan-950/60 px-3 py-1 text-xs text-cyan-100">
              <span class="relative flex size-2">
                <span class="absolute inline-flex size-full animate-ping rounded-full bg-cyan-400 opacity-75" />
                <span class="relative inline-flex size-2 rounded-full bg-cyan-400" />
              </span>
              {{ t('home.badge') }}
            </span>
            <div class="home-in mt-6" style="--d: 80ms">
              <h1 class="home-title text-4xl leading-tight font-bold tracking-tight break-words sm:text-6xl" :aria-label="name">
                <span aria-hidden="true">{{ title }}</span>
              </h1>
            </div>
            <p class="home-in mt-5 text-base leading-relaxed text-slate-300/85 sm:text-lg" style="--d: 160ms">{{ desc }}</p>
            <div class="home-in mt-9 flex flex-wrap items-center justify-center gap-3 lg:justify-start" style="--d: 240ms">
              <RouterLink :to="startTo" class="home-btn group h-11 rounded-lg px-6 text-base">
                {{ t('home.start') }}
                <ArrowRightIcon class="size-4 transition-transform group-hover:translate-x-0.5 rtl:-scale-x-100 rtl:group-hover:-translate-x-0.5" />
              </RouterLink>
              <RouterLink
                to="/login"
                class="inline-flex h-11 items-center gap-2 rounded-lg border border-white/15 bg-slate-900/60 px-6 text-base font-medium text-slate-100 transition hover:border-cyan-300/40 hover:bg-white/10"
              >
                {{ t('home.login') }}
              </RouterLink>
            </div>
            <div class="home-in mt-10 flex flex-wrap justify-center gap-x-4 gap-y-2 font-mono text-[11px] tracking-wider text-cyan-200/55 uppercase lg:justify-start" style="--d: 320ms">
              <span v-for="p in protocols" :key="p">{{ p }}</span>
            </div>
          </div>
        </div>
      </div>

      <!-- 仪表读数 -->
      <div class="home-hud pointer-events-none absolute end-8 bottom-24 z-10 hidden font-mono text-[11px] text-cyan-200/70 lg:block" dir="ltr">
        <div class="flex gap-5">
          <span>LON <b class="font-medium text-cyan-100">{{ coord(stats.lon, 'E', 'W') }}</b></span>
          <span>LAT <b class="font-medium text-cyan-100">{{ coord(stats.lat, 'N', 'S') }}</b></span>
          <span>LINKS <b class="font-medium text-cyan-100">{{ String(stats.links).padStart(2, '0') }}</b></span>
        </div>
        <div class="mt-2 h-px w-full bg-gradient-to-r from-cyan-400/60 via-cyan-400/10 to-transparent" />
        <div class="mt-1.5 tracking-[0.3em] text-cyan-300/45">{{ t('home.hud') }}</div>
      </div>

      <a href="#features" class="home-in absolute bottom-6 left-1/2 z-10 hidden -translate-x-1/2 flex-col items-center gap-1.5 text-[11px] text-slate-400 transition hover:text-cyan-200 sm:flex" style="--d: 500ms">
        <MouseIcon class="home-bob size-5" />
        {{ t('home.scroll') }}
      </a>
    </section>
    <div class="pointer-events-none relative -mt-24 h-24 bg-gradient-to-b from-transparent to-background" />

    <!-- 特性 -->
    <section id="features" class="mx-auto max-w-7xl scroll-mt-8 px-5 py-20 sm:px-8 sm:py-28">
      <div v-reveal class="mx-auto max-w-2xl text-center">
        <div class="font-mono text-xs tracking-[0.3em] text-primary uppercase">{{ t('home.featuresEyebrow') }}</div>
        <h2 class="mt-3 text-3xl font-bold tracking-tight sm:text-4xl">{{ t('home.featuresTitle') }}</h2>
        <p class="mt-4 text-muted-foreground">{{ t('home.featuresDesc') }}</p>
      </div>
      <div class="mt-14 grid gap-5 sm:grid-cols-2 lg:grid-cols-3">
        <div v-for="(f, i) in features" :key="f.key" v-reveal="i * 70">
          <div
            class="group relative h-full overflow-hidden rounded-2xl border bg-card p-6 transition-[translate,box-shadow,border-color] duration-300 hover:-translate-y-1 hover:border-primary/30 hover:shadow-xl"
            @pointermove="spot"
          >
            <div class="home-spot pointer-events-none absolute inset-0 opacity-0 transition-opacity duration-300 group-hover:opacity-100" />
            <div class="relative grid size-11 place-items-center rounded-xl bg-primary/10 text-primary ring-1 ring-primary/20">
              <component :is="f.icon" class="size-5" />
            </div>
            <h3 class="relative mt-5 text-lg font-semibold">{{ t(`home.features.${f.key}.title`) }}</h3>
            <p class="relative mt-2 text-sm leading-relaxed text-muted-foreground">{{ t(`home.features.${f.key}.desc`) }}</p>
          </div>
        </div>
      </div>
    </section>

    <!-- 三步开始 -->
    <section class="border-y bg-muted/30">
      <div class="mx-auto max-w-7xl px-5 py-20 sm:px-8 sm:py-24">
        <div v-reveal class="mx-auto max-w-2xl text-center">
          <div class="font-mono text-xs tracking-[0.3em] text-primary uppercase">{{ t('home.stepsEyebrow') }}</div>
          <h2 class="mt-3 text-3xl font-bold tracking-tight sm:text-4xl">{{ t('home.stepsTitle') }}</h2>
        </div>
        <div class="relative mt-14 grid gap-10 lg:grid-cols-3 lg:gap-8">
          <div class="home-flow pointer-events-none absolute top-7 right-[16%] left-[16%] hidden h-px lg:block" />
          <div v-for="(s, i) in steps" :key="s.key" v-reveal="i * 120" class="relative text-center">
            <div class="relative mx-auto grid size-14 place-items-center rounded-2xl border bg-background text-primary shadow-sm">
              <component :is="s.icon" class="size-6" />
              <span class="absolute -top-2 -end-2 grid size-6 place-items-center rounded-full bg-primary font-mono text-[11px] font-semibold text-primary-foreground">{{ i + 1 }}</span>
            </div>
            <h3 class="mt-5 text-lg font-semibold">{{ t(`home.steps.${s.key}.title`) }}</h3>
            <p class="mx-auto mt-2 max-w-xs text-sm leading-relaxed text-muted-foreground">{{ t(`home.steps.${s.key}.desc`) }}</p>
          </div>
        </div>
      </div>
    </section>

    <!-- 结尾号召 -->
    <section class="mx-auto max-w-7xl px-5 py-20 sm:px-8 sm:py-24">
      <div v-reveal class="home-cta dark relative isolate overflow-hidden rounded-3xl px-6 py-16 text-center sm:px-12">
        <div class="home-grid pointer-events-none absolute inset-0 -z-10 opacity-70" />
        <h2 class="text-3xl font-bold tracking-tight text-slate-50 sm:text-4xl">{{ t('home.ctaTitle') }}</h2>
        <p class="mx-auto mt-4 max-w-xl text-slate-300/80">{{ t('home.ctaDesc') }}</p>
        <RouterLink :to="startTo" class="home-btn group mt-8 h-11 rounded-lg px-7 text-base">
          {{ t('home.start') }}
          <ArrowRightIcon class="size-4 transition-transform group-hover:translate-x-0.5 rtl:-scale-x-100 rtl:group-hover:-translate-x-0.5" />
        </RouterLink>
      </div>
    </section>

    <footer class="border-t">
      <div class="mx-auto flex max-w-7xl flex-col items-center justify-between gap-3 px-5 py-8 text-sm text-muted-foreground sm:flex-row sm:px-8">
        <span>© {{ year }} {{ name }} · {{ t('home.rights') }}</span>
        <div class="flex items-center gap-5">
          <a v-if="cfg.site.terms_url" :href="cfg.site.terms_url" target="_blank" rel="noopener" class="hover:text-foreground">{{ t('auth.terms') }}</a>
          <RouterLink to="/login" class="hover:text-foreground">{{ t('auth.login') }}</RouterLink>
          <RouterLink v-if="canRegister" to="/register" class="hover:text-foreground">{{ t('auth.register') }}</RouterLink>
        </div>
      </div>
    </footer>
  </div>
</template>

<style scoped>
.home-aurora {
  background:
    radial-gradient(60% 50% at 75% 45%, rgb(14 165 233 / 0.16), transparent 70%),
    radial-gradient(40% 40% at 15% 20%, rgb(168 85 247 / 0.12), transparent 70%),
    radial-gradient(50% 40% at 50% 110%, rgb(34 211 238 / 0.12), transparent 70%);
}
.home-grid {
  background-image:
    linear-gradient(rgb(56 189 248 / 0.07) 1px, transparent 1px),
    linear-gradient(90deg, rgb(56 189 248 / 0.07) 1px, transparent 1px);
  background-size: 56px 56px;
  -webkit-mask-image: radial-gradient(ellipse 80% 70% at 50% 40%, #000 25%, transparent 75%);
  mask-image: radial-gradient(ellipse 80% 70% at 50% 40%, #000 25%, transparent 75%);
}
/* 静态星空：几层不同间距的平铺小点，不参与动画 */
.home-stars {
  background-image:
    radial-gradient(1px 1px at 23px 41px, rgb(203 213 225 / 0.55), transparent),
    radial-gradient(1px 1px at 131px 87px, rgb(203 213 225 / 0.4), transparent),
    radial-gradient(1.5px 1.5px at 77px 153px, rgb(165 243 252 / 0.7), transparent),
    radial-gradient(1px 1px at 189px 19px, rgb(203 213 225 / 0.35), transparent),
    radial-gradient(1px 1px at 53px 211px, rgb(203 213 225 / 0.3), transparent),
    radial-gradient(1.5px 1.5px at 163px 239px, rgb(203 213 225 / 0.5), transparent);
  background-size:
    220px 230px,
    260px 210px,
    310px 290px,
    240px 270px,
    280px 250px,
    350px 330px;
}
.home-scan {
  will-change: transform;
  height: 160px;
  background: linear-gradient(to bottom, transparent, rgb(34 211 238 / 0.06) 70%, rgb(103 232 249 / 0.18) 99%, transparent);
  animation: home-scan 7s cubic-bezier(0.4, 0, 0.6, 1) infinite;
}
.home-title {
  background: linear-gradient(100deg, #f8fafc 10%, #a5f3fc 45%, #60a5fa 75%, #c084fc 100%);
  background-size: 200% 100%;
  -webkit-background-clip: text;
  background-clip: text;
  color: transparent;
  text-shadow: 0 0 40px rgb(34 211 238 / 0.15);
  animation: home-shine 8s ease-in-out infinite alternate;
}
.home-logo {
  background: linear-gradient(135deg, rgb(34 211 238 / 0.35), rgb(99 102 241 / 0.35));
  box-shadow: inset 0 0 0 1px rgb(103 232 249 / 0.35), 0 0 18px rgb(34 211 238 / 0.35);
}
.home-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 0.5rem;
  font-weight: 500;
  color: #fff;
  background: linear-gradient(135deg, #06b6d4, #3b82f6 55%, #6366f1);
  box-shadow: 0 0 0 1px rgb(103 232 249 / 0.35) inset, 0 8px 30px -8px rgb(34 211 238 / 0.65);
  transition: box-shadow 0.25s, transform 0.25s, filter 0.25s;
}
.home-btn:hover {
  filter: brightness(1.1);
  box-shadow: 0 0 0 1px rgb(165 243 252 / 0.6) inset, 0 10px 40px -6px rgb(34 211 238 / 0.85);
}
.home-btn:active {
  transform: translateY(1px);
}
.home-hud::before,
.home-hud::after {
  content: '';
  position: absolute;
  width: 10px;
  height: 10px;
  border-color: rgb(103 232 249 / 0.5);
}
.home-hud::before {
  top: -10px;
  left: -12px;
  border-top: 1px solid;
  border-left: 1px solid;
}
.home-hud::after {
  bottom: -6px;
  right: -12px;
  border-bottom: 1px solid;
  border-right: 1px solid;
}
.home-in {
  animation: home-in 0.9s cubic-bezier(0.2, 0.7, 0.2, 1) both;
  animation-delay: var(--d, 0ms);
}
.home-bob {
  animation: home-bob 2s ease-in-out infinite;
}
.home-spot {
  background: radial-gradient(360px circle at var(--x, 50%) var(--y, 50%), color-mix(in oklch, var(--primary) 16%, transparent), transparent 45%);
}
.home-flow {
  background: linear-gradient(90deg, transparent, var(--primary), transparent);
  background-size: 50% 100%;
  background-repeat: no-repeat;
  opacity: 0.6;
  animation: home-flow 3.2s linear infinite;
}
.home-flow::before {
  content: '';
  position: absolute;
  inset: 0;
  background: repeating-linear-gradient(90deg, color-mix(in oklch, var(--primary) 35%, transparent) 0 6px, transparent 6px 12px);
  opacity: 0.5;
}
.home-cta {
  background: #050b1a;
}
.home-cta::before {
  content: '';
  position: absolute;
  inset: -60%;
  z-index: -2;
  background: conic-gradient(from 0deg, transparent 0 62%, #22d3ee 75%, #a855f7 85%, transparent 95%);
  animation: home-spin 7s linear infinite;
}
.home-cta::after {
  content: '';
  position: absolute;
  inset: 1px;
  z-index: -1;
  border-radius: calc(1.5rem - 1px);
  background:
    radial-gradient(60% 80% at 50% 0%, rgb(14 165 233 / 0.22), transparent 70%),
    #050b1a;
}

@keyframes home-scan {
  from {
    transform: translateY(-160px);
  }
  to {
    transform: translateY(100svh);
  }
}
@keyframes home-shine {
  to {
    background-position: 100% 0;
  }
}
@keyframes home-in {
  from {
    opacity: 0;
    transform: translateY(18px);
  }
}
@keyframes home-bob {
  50% {
    transform: translateY(4px);
  }
}
@keyframes home-flow {
  from {
    background-position: -50% 0;
  }
  to {
    background-position: 150% 0;
  }
}
@keyframes home-spin {
  to {
    transform: rotate(1turn);
  }
}

:global(.home-reveal) {
  opacity: 0;
  transform: translateY(28px);
  transition:
    opacity 0.8s cubic-bezier(0.2, 0.7, 0.2, 1),
    transform 0.8s cubic-bezier(0.2, 0.7, 0.2, 1);
}
:global(.home-reveal.is-in) {
  opacity: 1;
  transform: none;
}

@media (prefers-reduced-motion: reduce) {
  .home-scan,
  .home-title,
  .home-in,
  .home-bob,
  .home-flow,
  .home-cta::before {
    animation: none;
  }
  :global(.home-reveal) {
    opacity: 1;
    transform: none;
    transition: none;
  }
}
</style>
