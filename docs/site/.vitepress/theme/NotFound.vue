<script setup lang="ts">
import { useData, withBase } from 'vitepress'
import { computed } from 'vue'

import CreeperFace from './CreeperFace.vue'

// The page of a missing page, as VitePress's own, under a creeper that
// blew it up.
const { theme, localeIndex } = useData()
const home = computed(() => withBase(localeIndex.value === 'root' ? '/' : `/${localeIndex.value}/`))
</script>

<template>
  <div class="NotFound">
    <CreeperFace class="creeper" />
    <p class="code">404</p>
    <h1 class="title">{{ theme.notFound?.title ?? 'PAGE NOT FOUND' }}</h1>
    <div class="divider" />
    <blockquote class="quote">{{ theme.notFound?.quote }}</blockquote>
    <div class="action">
      <a class="link" :href="home" :aria-label="theme.notFound?.linkLabel">{{ theme.notFound?.linkText ?? 'Take me home' }}</a>
    </div>
  </div>
</template>

<style scoped>
.NotFound {
  padding: 64px 24px 96px;
  text-align: center;
}

@media (min-width: 768px) {
  .NotFound {
    padding: 96px 32px 168px;
  }
}

.creeper {
  width: 64px;
  height: 64px;
  margin: 0 auto 24px;
}

@media (prefers-reduced-motion: no-preference) {
  .creeper {
    animation: jec-hiss 1.4s ease-in-out infinite alternate;
  }
}

@keyframes jec-hiss {
  to {
    scale: 1.08;
  }
}

.code {
  line-height: 64px;
  font-size: 64px;
  font-weight: 600;
}

.title {
  padding-top: 12px;
  letter-spacing: 2px;
  line-height: 20px;
  font-size: 20px;
  font-weight: 700;
}

.divider {
  margin: 24px auto 18px;
  width: 64px;
  height: 1px;
  background-color: var(--vp-c-divider);
}

.quote {
  margin: 0 auto;
  max-width: 320px;
  font-size: 14px;
  font-weight: 500;
  color: var(--vp-c-text-2);
}

.action {
  padding-top: 20px;
}

.link {
  display: inline-block;
  border: 1px solid var(--vp-c-brand-1);
  border-radius: 16px;
  padding: 3px 16px;
  font-size: 14px;
  font-weight: 500;
  color: var(--vp-c-brand-1);
  transition:
    border-color 0.25s,
    color 0.25s;
}

.link:hover {
  border-color: var(--vp-c-brand-2);
  color: var(--vp-c-brand-2);
}
</style>
