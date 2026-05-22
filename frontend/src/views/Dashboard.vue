<template>
  <div>
    <div class="page-title">
      总览
      <span class="subtitle">每 10 秒自动刷新</span>
    </div>
    <div class="summary-row">
      <div class="summary-card accent">
        <div class="label">启用策略</div>
        <div class="value">{{ summary.policy_enabled }}</div>
      </div>
      <div class="summary-card">
        <div class="label">运行中任务</div>
        <div class="value">{{ runningTasks }}</div>
      </div>
      <div class="summary-card danger">
        <div class="label">漏洞总数</div>
        <div class="value">{{ vulnTotal }}</div>
      </div>
      <div class="summary-card warning">
        <div class="label">弱口令命中</div>
        <div class="value">{{ weakTotal }}</div>
      </div>
      <div class="summary-card">
        <div class="label">K3s Node</div>
        <div class="value">{{ summary.node_total }}</div>
      </div>
      <div class="summary-card">
        <div class="label">Scanner Pod</div>
        <div class="value">{{ summary.pod_total }}</div>
      </div>
    </div>

    <div class="section-card">
      <div class="card-head">
        <div>
          <div class="card-title">最近任务</div>
          <div class="card-sub">展示最近 10 条任务的运行状态</div>
        </div>
        <el-button size="small" @click="$router.push('/tasks')">查看全部</el-button>
      </div>
      <el-table :data="tasks" stripe size="small">
        <el-table-column label="ID" width="80">
          <template #default="{ row }"><span class="mono">#{{ row.id }}</span></template>
        </el-table-column>
        <el-table-column prop="name" label="任务名" />
        <el-table-column label="状态" width="110">
          <template #default="{ row }">
            <el-tag :type="statusType(row.status)" size="small" effect="light">{{ row.status }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="alive_total" label="存活" width="80" />
        <el-table-column prop="part_total" label="分片" width="80" />
        <el-table-column prop="created_at" label="创建时间" width="200">
          <template #default="{ row }"><span class="mono">{{ fmt(row.created_at) }}</span></template>
        </el-table-column>
      </el-table>
    </div>
  </div>
</template>

<script setup>
import { onMounted, reactive, ref, computed, onUnmounted } from 'vue'
import { resSummary, listTasks } from '@/api'

const summary = reactive({ policy_enabled: 0, node_total: 0, pod_total: 0 })
const tasks = ref([])
const runningTasks = computed(() => tasks.value.filter((t) => t.status === 'running').length)
const vulnTotal = ref(0)
const weakTotal = ref(0)
let timer = null

async function refresh() {
  try {
    const s = await resSummary()
    Object.assign(summary, s)
  } catch (e) {}
  try {
    const r = await listTasks({ page: 1, page_size: 10 })
    tasks.value = r.items || []
  } catch (e) {}
}

function statusType(s) {
  if (s === 'running' || s === 'completed') return 'success'
  if (s === 'paused' || s === 'restarting') return 'warning'
  if (s === 'failed') return 'danger'
  if (s === 'terminated') return 'info'
  return 'info'
}

function fmt(t) {
  if (!t) return '—'
  const d = new Date(t)
  if (isNaN(d.getTime())) return t
  return d.toLocaleString('zh-CN', { hour12: false, timeZone: 'Asia/Shanghai' })
}

onMounted(() => { refresh(); timer = setInterval(refresh, 10000) })
onUnmounted(() => clearInterval(timer))
</script>
