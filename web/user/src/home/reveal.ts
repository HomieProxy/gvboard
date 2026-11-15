import type { Directive } from 'vue'

// 元素滚动进入视野时淡入上浮；绑定值为延迟毫秒数。
let io: IntersectionObserver | null = null

function observer() {
  io ??= new IntersectionObserver(
    (entries) => {
      for (const e of entries) {
        if (!e.isIntersecting) continue
        e.target.classList.add('is-in')
        io?.unobserve(e.target)
      }
    },
    { rootMargin: '0px 0px -8% 0px', threshold: 0.12 },
  )
  return io
}

export const vReveal: Directive<HTMLElement, number | undefined> = {
  mounted(el, binding) {
    el.classList.add('home-reveal')
    if (binding.value) el.style.transitionDelay = `${binding.value}ms`
    observer().observe(el)
  },
  unmounted(el) {
    io?.unobserve(el)
  },
}
