<template>
  <div class="shell">
    <aside class="shell-side">
      <div class="brand">
        <span class="logo-dot"></span>
        DAST
      </div>
      <nav>
        <router-link v-for="m in menu" :key="m.path" :to="m.path">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <path :d="m.icon" />
          </svg>
          {{ m.label }}
        </router-link>
      </nav>
      <div class="side-footer">
        <div style="margin-bottom:6px; display:flex; align-items:center;">
          <span class="status-dot" :class="health.healthy ? 'green' : 'red'"></span>
          后端 {{ health.healthy ? 'OK' : '不可达' }}
        </div>
        <div style="opacity:0.7">v0.1.0 · MVP</div>
      </div>
    </aside>

    <section class="shell-main">
      <header class="shell-top">
        <div class="crumb">{{ crumb }}</div>
        <div class="top-right">
          <el-tag size="small" type="info" effect="plain">
            <span style="font-weight:500">{{ user?.username || '已登录' }}</span>
          </el-tag>
          <el-button size="small" plain @click="onLogout">退出登录</el-button>
        </div>
      </header>
      <main class="shell-content">
        <router-view />
      </main>
    </section>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import http from '@/api/http'

const route = useRoute()
const router = useRouter()
const health = reactive({ healthy: false })
const user = JSON.parse(localStorage.getItem('dast_user') || 'null')

// Heroicons-style outline paths
const menu = [
  { path: '/dashboard', label: '总览', icon: 'M3 13.5V19a2 2 0 0 0 2 2h4M15 21h4a2 2 0 0 0 2-2v-5.5M3 10.5 12 3l9 7.5' },
  { path: '/policies',  label: '策略管理', icon: 'M12 2 4 6v6c0 5 3.5 9.5 8 10 4.5-0.5 8-5 8-10V6l-8-4z' },
  { path: '/tasks',     label: '任务管理', icon: 'M4 5h16M4 12h16M4 19h10' },
  { path: '/resources', label: '资源情况', icon: 'M3 7h18M3 12h18M3 17h18M7 4v16M17 4v16' }
]

const labelByPath = Object.fromEntries(menu.map(m => [m.path, m.label]))
const crumb = computed(() => {
  for (const m of menu) if (route.path.startsWith(m.path)) return labelByPath[m.path]
  return 'DAST'
})

async function check() {
  try { await http.get('/health'); health.healthy = true } catch { health.healthy = false }
}
onMounted(() => { check(); setInterval(check, 10000) })

function onLogout() {
  localStorage.removeItem('dast_token')
  localStorage.removeItem('dast_user')
  router.replace('/login')
}
</script>
