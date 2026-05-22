<template>
  <div class="login-page">
    <div class="login-box">
      <div class="brand-row">
        <span class="logo-dot"></span>
        <span class="brand-text">DAST</span>
      </div>
      <div class="hint">K3s 分布式 DAST · 控制台</div>
      <el-form @submit.prevent="submit" size="large">
        <el-form-item>
          <el-input v-model="form.username" placeholder="用户名" :prefix-icon="UserIcon" />
        </el-form-item>
        <el-form-item>
          <el-input v-model="form.password" type="password" placeholder="密码" show-password :prefix-icon="LockIcon" />
        </el-form-item>
        <el-button type="primary" :loading="loading" style="width:100%; height:42px; font-weight:600; letter-spacing:0.04em" @click="submit">
          登 录
        </el-button>
      </el-form>
    </div>
  </div>
</template>

<script setup>
import { reactive, ref, h } from 'vue'
import { useRouter } from 'vue-router'
import { login } from '@/api'
import { ElMessage } from 'element-plus'

const router = useRouter()
const form = reactive({ username: '', password: '' })
const loading = ref(false)

const UserIcon = () => h('svg', { viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor', 'stroke-width': 1.8, style: 'width:16px; height:16px' }, [
  h('circle', { cx: 12, cy: 8, r: 4 }),
  h('path', { d: 'M4 21c0-4.4 3.6-8 8-8s8 3.6 8 8' })
])
const LockIcon = () => h('svg', { viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor', 'stroke-width': 1.8, style: 'width:16px; height:16px' }, [
  h('rect', { x: 4, y: 11, width: 16, height: 10, rx: 2 }),
  h('path', { d: 'M8 11V7a4 4 0 0 1 8 0v4' })
])

async function submit() {
  if (!form.username || !form.password) {
    ElMessage.warning('请输入用户名和密码')
    return
  }
  loading.value = true
  try {
    const data = await login(form.username, form.password)
    localStorage.setItem('dast_token', data.token)
    localStorage.setItem('dast_user', JSON.stringify(data.user))
    router.replace('/dashboard')
  } finally {
    loading.value = false
  }
}
</script>
