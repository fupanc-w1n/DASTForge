<template>
  <div>
    <div class="page-title">
      资源情况
      <div class="actions">
        <el-button @click="refresh">刷新</el-button>
      </div>
    </div>

    <div class="summary-row">
      <div class="summary-card"><div class="label">Node</div><div class="value">{{ summary.node_total }}</div></div>
      <div class="summary-card"><div class="label">Pod 总数</div><div class="value">{{ summary.pod_total }}</div></div>
      <div class="summary-card success"><div class="label">Running</div><div class="value">{{ summary.pod_running }}</div></div>
      <div class="summary-card danger"><div class="label">异常 Pod</div><div class="value">{{ summary.pod_abnormal }}</div></div>
      <div class="summary-card accent"><div class="label">启用策略</div><div class="value">{{ summary.policy_enabled }}</div></div>
      <div class="summary-card" :class="summary.redis_ok ? 'success' : 'danger'">
        <div class="label">Redis</div>
        <div class="value" style="font-size:18px;">
          <el-tag :type="summary.redis_ok ? 'success' : 'danger'" size="small" effect="light">{{ summary.redis_ok ? 'OK' : 'DOWN' }}</el-tag>
        </div>
      </div>
      <div class="summary-card" :class="summary.mysql_ok ? 'success' : 'danger'">
        <div class="label">MySQL</div>
        <div class="value" style="font-size:18px;">
          <el-tag :type="summary.mysql_ok ? 'success' : 'danger'" size="small" effect="light">{{ summary.mysql_ok ? 'OK' : 'DOWN' }}</el-tag>
        </div>
      </div>
    </div>

    <div class="section-card">
      <div class="card-head">
        <div>
          <div class="card-title">K3s 节点</div>
          <div class="card-sub">集群中所有 Node 的 CPU / 内存 / 就绪状态</div>
        </div>
      </div>
      <el-table :data="nodes" size="small" stripe>
        <el-table-column prop="name" label="节点名" min-width="200" />
        <el-table-column label="Internal IP" width="160"><template #default="{ row }"><span class="mono">{{ row.internal_ip }}</span></template></el-table-column>
        <el-table-column prop="cpu_capacity" label="CPU" width="80" />
        <el-table-column prop="memory_capacity" label="Mem" width="120" />
        <el-table-column prop="os_image" label="OS" min-width="200" />
        <el-table-column label="Kubelet" width="170"><template #default="{ row }"><span class="mono">{{ row.kubelet_version }}</span></template></el-table-column>
        <el-table-column label="Ready" width="100">
          <template #default="{ row }">
            <el-tag :type="row.ready ? 'success' : 'danger'" size="small" effect="light">
              {{ row.ready ? 'Ready' : 'NotReady' }}
            </el-tag>
          </template>
        </el-table-column>
        <template #empty><div class="empty-state"><div class="muted">无 Node 数据(沙箱无集群)</div></div></template>
      </el-table>
    </div>

    <div class="section-card">
      <div class="card-head">
        <div>
          <div class="card-title">扫描器 Pod</div>
          <div class="card-sub">所有 <code class="mono">app.kubernetes.io/name=dast-scanner</code> 标签的 Pod</div>
        </div>
      </div>
      <el-table :data="pods" size="small" stripe>
        <el-table-column prop="namespace" label="Namespace" width="120" />
        <el-table-column label="Pod">
          <template #default="{ row }"><span class="mono">{{ row.name }}</span></template>
        </el-table-column>
        <el-table-column label="策略" width="80">
          <template #default="{ row }"><span class="mono">#{{ row.policy_id }}</span></template>
        </el-table-column>
        <el-table-column label="模块" width="120">
          <template #default="{ row }">
            <el-tag size="small" :type="moduleTagType(row.module)" effect="plain">{{ moduleLabel[row.module] || row.module }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="IP" width="140"><template #default="{ row }"><span class="mono">{{ row.pod_ip }}</span></template></el-table-column>
        <el-table-column prop="node_name" label="Node" width="160" />
        <el-table-column label="Phase" width="110">
          <template #default="{ row }">
            <el-tag size="small" :type="phaseType(row.phase, row.ready)" effect="light">{{ row.phase }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="restart_count" label="重启" width="80" />
        <template #empty><div class="empty-state"><div class="muted">还没有 Scanner Pod(启用策略后会自动部署)</div></div></template>
      </el-table>
    </div>
  </div>
</template>

<script setup>
import { onMounted, onUnmounted, reactive, ref } from 'vue'
import { resSummary, resNodes, resPods } from '@/api'

const summary = reactive({ node_total: 0, pod_total: 0, pod_running: 0, pod_abnormal: 0, policy_enabled: 0, redis_ok: false, mysql_ok: false })
const nodes = ref([])
const pods = ref([])
let timer = null

const moduleLabel = { portscan: '端口扫描', nmap: '服务识别', nuclei: '漏洞扫描', weakpass: '弱口令' }
function moduleTagType(m) {
  if (m === 'nuclei') return 'danger'
  if (m === 'weakpass') return 'warning'
  if (m === 'nmap') return 'info'
  return ''
}
function phaseType(phase, ready) {
  if (phase === 'Running' && ready) return 'success'
  if (phase === 'Pending') return 'warning'
  if (phase === 'Failed') return 'danger'
  return 'info'
}

async function refresh() {
  try {
    Object.assign(summary, await resSummary())
    nodes.value = (await resNodes()).items || []
    pods.value = (await resPods()).items || []
  } catch (e) {}
}

onMounted(() => { refresh(); timer = setInterval(refresh, 10000) })
onUnmounted(() => clearInterval(timer))
</script>
