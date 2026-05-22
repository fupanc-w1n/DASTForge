<template>
  <div>
    <div class="page-title">
      策略管理
      <div class="actions">
        <el-button @click="refresh">刷新</el-button>
        <el-button type="primary" @click="$router.push('/policies/new')">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" style="width:14px; height:14px; margin-right:6px;"><path d="M12 5v14M5 12h14"/></svg>
          新建策略
        </el-button>
      </div>
    </div>

    <div class="section-card">
      <el-table :data="rows" v-loading="loading" stripe>
        <el-table-column label="ID" width="80">
          <template #default="{ row }"><span class="mono">#{{ row.id }}</span></template>
        </el-table-column>
        <el-table-column prop="name" label="策略名称" min-width="180" />
        <el-table-column label="状态" width="110">
          <template #default="{ row }">
            <el-tag :type="statusTagType(row)" size="small" effect="light">
              <span class="status-dot" :class="row.enabled ? 'green' : 'gray'" style="margin-right:4px;"></span>
              {{ row.status }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="启用模块" min-width="320">
          <template #default="{ row }">
            <el-tag v-for="m in row.enabled_modules" :key="m" size="small" effect="plain" style="margin-right:4px">
              {{ moduleLabel[m] || m }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="最近部署" width="180">
          <template #default="{ row }"><span class="mono">{{ fmt(row.last_deployed_at) }}</span></template>
        </el-table-column>
        <el-table-column label="创建时间" width="180">
          <template #default="{ row }"><span class="mono">{{ fmt(row.created_at) }}</span></template>
        </el-table-column>
        <el-table-column label="操作" width="260" fixed="right">
          <template #default="{ row }">
            <el-button size="small" plain @click="$router.push(`/policies/${row.id}`)">查看</el-button>
            <el-button size="small" :type="row.enabled ? 'warning' : 'success'"
                       @click="row.enabled ? onDisable(row) : onEnable(row)">
              {{ row.enabled ? '停用' : '启用' }}
            </el-button>
            <el-button size="small" type="danger" plain @click="onDelete(row)">删除</el-button>
          </template>
        </el-table-column>
        <template #empty>
          <div class="empty-state">
            <div class="muted">还没有策略,点右上角"新建策略"开始</div>
          </div>
        </template>
      </el-table>
    </div>
  </div>
</template>

<script setup>
import { onMounted, ref } from 'vue'
import { listPolicies, enablePolicy, disablePolicy, deletePolicy } from '@/api'
import { ElMessageBox, ElMessage } from 'element-plus'

const rows = ref([])
const loading = ref(false)
const moduleLabel = { portscan: '端口扫描', nmap: '服务识别', nuclei: '漏洞扫描', weakpass: '弱口令' }

async function refresh() {
  loading.value = true
  try {
    const r = await listPolicies()
    rows.value = r.items || []
  } finally {
    loading.value = false
  }
}

function statusTagType(row) {
  if (row.enabled) return 'success'
  if (row.status === 'error') return 'danger'
  return 'info'
}

function fmt(t) {
  if (!t) return '—'
  const d = new Date(t)
  if (isNaN(d.getTime())) return t
  return d.toLocaleString('zh-CN', { hour12: false, timeZone: 'Asia/Shanghai' })
}

async function onEnable(row) {
  await ElMessageBox.confirm('启用即部署到 K3s(创建 ConfigMap + Deployment),确认继续?', '启用并部署', { type: 'info' })
  await enablePolicy(row.id); ElMessage.success('已启用并部署'); refresh()
}
async function onDisable(row) {
  await ElMessageBox.confirm('停用会把策略下所有 Deployment 副本数缩到 0,确认继续?', '停用', { type: 'warning' })
  await disablePolicy(row.id); ElMessage.success('已停用'); refresh()
}
async function onDelete(row) {
  await ElMessageBox.confirm('删除策略将一并清理 ConfigMap 和 Deployment,确认?', '删除', { type: 'warning' })
  await deletePolicy(row.id); ElMessage.success('已删除'); refresh()
}

onMounted(refresh)
</script>
