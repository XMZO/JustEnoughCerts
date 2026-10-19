<script setup lang="ts">
import { useData, withBase } from 'vitepress'
import { computed } from 'vue'

// The overview of the panel, below the features of the home page: in the
// page's language, light or dark with the site. The pictures are taken
// from a JEC with a few certificates (public/preview/).
const { lang } = useData()

const zh = computed(() => lang.value === 'zh-CN')
const text = computed(() =>
  zh.value
    ? {
        title: '一眼看清每张证书',
        details: '总览按剩余天数排列全部证书，最需要处理的在前；需要处理的事项、未来 90 天的到期时间与最近动态一目了然。',
        alt: 'JEC 的总览页',
      }
    : {
        title: 'Every certificate at a glance',
        details:
          'The overview lists every certificate by days left, the most urgent first, with what needs attention, what expires in the next 90 days and the latest activity.',
        alt: 'The overview of JEC',
      },
)

function picture(theme: 'light' | 'dark'): string {
  return withBase(`/preview/overview-${zh.value ? 'zh-CN' : 'en-US'}-${theme}.webp`)
}
</script>

<template>
  <section class="JecPreview">
    <div class="container">
      <h2 class="title">{{ text.title }}</h2>
      <p class="details">{{ text.details }}</p>
      <figure class="window">
        <div class="bar" aria-hidden="true"><span /><span /><span /></div>
        <img class="light" :src="picture('light')" :alt="text.alt" width="2560" height="1600" loading="lazy" decoding="async" />
        <img class="dark" :src="picture('dark')" :alt="text.alt" width="2560" height="1600" loading="lazy" decoding="async" />
      </figure>
    </div>
  </section>
</template>

<style scoped>
.JecPreview {
  padding: 64px 24px 0;
}

@media (min-width: 640px) {
  .JecPreview {
    padding: 96px 48px 0;
  }
}

@media (min-width: 960px) {
  .JecPreview {
    padding: 112px 64px 0;
  }
}

.container {
  margin: 0 auto;
  max-width: 1152px;
  text-align: center;
}

.title {
  margin: 0;
  line-height: 36px;
  font-size: 28px;
  font-weight: 700;
  letter-spacing: -0.4px;
  color: var(--vp-c-text-1);
}

.details {
  margin: 12px auto 40px;
  max-width: 640px;
  line-height: 28px;
  font-size: 16px;
  color: var(--vp-c-text-2);
}

.window {
  margin: 0;
  overflow: hidden;
  border: 1px solid var(--vp-c-divider);
  border-radius: 12px;
  background-color: var(--vp-c-bg-alt);
  box-shadow: var(--jec-window-shadow);
}

.bar {
  display: flex;
  gap: 6px;
  border-bottom: 1px solid var(--vp-c-divider);
  padding: 10px 14px;
}

.bar span {
  border-radius: 50%;
  width: 10px;
  height: 10px;
  background-color: var(--vp-c-divider);
}

img {
  display: block;
  width: 100%;
  height: auto;
}

/* The window rises into view as the page scrolls to it, where the browser
   ties animations to scrolling. */
@supports (animation-timeline: view()) {
  @media (prefers-reduced-motion: no-preference) {
    .window {
      animation: jec-reveal linear both;
      animation-timeline: view();
      animation-range: entry 0% entry 70%;
    }
  }
}

@keyframes jec-reveal {
  from {
    opacity: 0;
    transform: translateY(48px) scale(0.96);
  }
  to {
    opacity: 1;
    transform: none;
  }
}
</style>
