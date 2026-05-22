<template>
  <div v-if="task">
    <div class="page-title">
      <div>
        任务 <span class="mono">#{{ task.id }}</span>
        <span class="subtitle">{{ task.name }}</span>
      </div>
      <div class="actions">
        <el-button @click="$router.push('/tasks')">返回</el-button>
        <el-button v-if="task.status === 'running'" @click="action('pause')">暂停</el-button>
        <el-button v-if="task.status === 'paused'" type="success" @click="action('resume')">继续</el-button>
        <el-button type="danger" plain v-if="['running','paused','queued','restarting'].includes(task.status)" @click="action('terminate')">终止</el-button>
        <el-button v-if="['completed','terminated','failed'].includes(task.status)" @click="action('restart')">重启</el-button>
        <el-button @click="refresh">刷新</el-button>
      </div>
    </div>

    <div class="summary-row">
      <div class="summary-card">
        <div class="label">状态</div>
        <div class="value" style="font-size:18px;">
          <el-tag :type="statusType(task.status)" size="default" effect="light">{{ task.status }}</el-tag>
        </div>
      </div>
      <div class="summary-card">
        <div class="label">目标 / 存活</div>
        <div class="value">{{ task.alive_total }} <span style="font-size:14px; color:var(--text-muted)">/ {{ task.target_total }}</span></div>
      </div>
      <div class="summary-card accent">
        <div class="label">完成分片</div>
        <div class="value">{{ progress.part_completed || 0 }} <span style="font-size:14px; color:var(--text-muted)">/ {{ progress.part_total || 0 }}</span></div>
      </div>
      <div class="summary-card">
        <div class="label">开放端口</div>
        <div class="value">{{ progress.port_open_total || 0 }}</div>
      </div>
      <div class="summary-card danger">
        <div class="label">漏洞</div>
        <div class="value">{{ progress.vulnerability_total || 0 }}</div>
      </div>
      <div class="summary-card warning">
        <div class="label">弱口令</div>
        <div class="value">{{ progress.weak_password_total || 0 }}</div>
      </div>
    </div>

    <div class="section-card">
      <el-tabs v-model="tab">
        <el-tab-pane label="扫描分片" name="parts">
          <el-table :data="parts" size="small" stripe>
            <el-table-column label="Part">
              <template #default="{ row }"><span class="mono">{{ row.task_part_name }}</span></template>
            </el-table-column>
            <el-table-column prop="host_count" label="Host" width="70" />
            <el-table-column label="Part 状态" width="120">
              <template #default="{ row }"><el-tag size="small" :type="statusType(row.status)" effect="light">{{ row.status }}</el-tag></template>
            </el-table-column>
            <el-table-column label="端口扫描" width="110">
              <template #default="{ row }"><PartStatus :s="row.portscan_status" /></template>
            </el-table-column>
            <el-table-column label="服务识别" width="110">
              <template #default="{ row }"><PartStatus :s="row.nmap_status" /></template>
            </el-table-column>
            <el-table-column label="Nuclei" width="110">
              <template #default="{ row }"><PartStatus :s="row.nuclei_status" /></template>
            </el-table-column>
            <el-table-column label="弱口令" width="110">
              <template #default="{ row }"><PartStatus :s="row.weakpass_status" /></template>
            </el-table-column>
            <el-table-column label="完成时间" width="170">
              <template #default="{ row }"><span class="mono">{{ fmt(row.completed_at) }}</span></template>
            </el-table-column>
          </el-table>
        </el-tab-pane>

        <el-tab-pane label="存活主机" name="alive">
          <el-table :data="aliveHostRows" size="small" stripe>
            <el-table-column label="Host">
              <template #default="{ row }"><span class="mono">{{ row.host }}</span></template>
            </el-table-column>
          </el-table>
        </el-tab-pane>

        <el-tab-pane label="开放端口" name="ports">
          <el-table :data="ports" size="small" stripe>
            <el-table-column label="Host"><template #default="{ row }"><span class="mono">{{ row.host }}</span></template></el-table-column>
            <el-table-column prop="port" label="Port" width="80" />
            <el-table-column prop="protocol" label="Protocol" width="100" />
            <el-table-column label="State" width="100">
              <template #default="{ row }"><el-tag size="small" type="success" effect="light">{{ row.state }}</el-tag></template>
            </el-table-column>
            <el-table-column label="发现时间" width="180"><template #default="{ row }"><span class="mono">{{ fmt(row.created_at) }}</span></template></el-table-column>
          </el-table>
        </el-tab-pane>

        <el-tab-pane label="服务识别" name="services">
          <el-table :data="services" size="small" stripe>
            <el-table-column label="Host"><template #default="{ row }"><span class="mono">{{ row.host }}</span></template></el-table-column>
            <el-table-column prop="port" label="Port" width="80" />
            <el-table-column prop="service" label="Service" width="120" />
            <el-table-column prop="product" label="Product" />
            <el-table-column prop="version" label="Version" width="120" />
          </el-table>
        </el-tab-pane>

        <el-tab-pane label="漏洞" name="vulns">
          <el-table :data="vulns" size="small" stripe>
            <el-table-column label="Severity" width="110">
              <template #default="{ row }"><el-tag size="small" :type="sevType(row.severity)" effect="light">{{ row.severity }}</el-tag></template>
            </el-table-column>
            <el-table-column label="Template" width="220">
              <template #default="{ row }"><span class="mono">{{ row.template_id }}</span></template>
            </el-table-column>
            <el-table-column prop="name" label="名称" />
            <el-table-column prop="matched" label="Matched" show-overflow-tooltip />
            <el-table-column label="Host" width="180"><template #default="{ row }"><span class="mono">{{ row.host }}</span></template></el-table-column>
            <el-table-column label="举证" width="90" fixed="right">
              <template #default="{ row }">
                <el-button link type="primary" size="small" @click="openVuln(row)">查看</el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-tab-pane>

        <el-tab-pane label="弱口令" name="weaks">
          <el-table :data="weaks" size="small" stripe>
            <el-table-column prop="service" label="Service" width="100">
              <template #default="{ row }"><el-tag size="small" effect="plain">{{ row.service }}</el-tag></template>
            </el-table-column>
            <el-table-column label="Host"><template #default="{ row }"><span class="mono">{{ row.host }}</span></template></el-table-column>
            <el-table-column prop="port" label="Port" width="80" />
            <el-table-column prop="username" label="Username" width="160"><template #default="{ row }"><span class="mono">{{ row.username || '—' }}</span></template></el-table-column>
            <el-table-column label="Password" width="220">
              <template #default="{ row }">
                <span class="mono" v-if="!revealed[row.id]">••••••</span>
                <span class="mono" v-else>{{ row.password }}</span>
                <el-button link type="primary" size="small" @click="revealed[row.id] = !revealed[row.id]" style="margin-left:8px;">
                  {{ revealed[row.id] ? '隐藏' : '显示' }}
                </el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-tab-pane>

        <el-tab-pane label="事件" name="events">
          <el-table :data="events" size="small" stripe>
            <el-table-column label="时间" width="180"><template #default="{ row }"><span class="mono">{{ fmt(row.created_at) }}</span></template></el-table-column>
            <el-table-column prop="module" label="模块" width="120" />
            <el-table-column label="等级" width="90">
              <template #default="{ row }"><el-tag size="small" :type="levelType(row.level)" effect="light">{{ row.level }}</el-tag></template>
            </el-table-column>
            <el-table-column prop="message" label="消息" />
          </el-table>
        </el-tab-pane>
      </el-tabs>
    </div>

    <transition name="evidence-slide">
      <aside v-if="vulnOpen && currentVuln" class="evidence-card">
        <div class="evidence-head">
          <div>
            <div class="evidence-title">漏洞举证</div>
            <div class="evidence-sub mono">{{ currentVuln.template_id || '—' }}</div>
          </div>
          <el-button size="small" text @click="vulnOpen = false">关闭</el-button>
        </div>
        <dl class="evidence-meta">
          <div><dt>Severity</dt><dd><el-tag :type="sevType(currentVuln.severity)" size="small" effect="light">{{ currentVuln.severity || 'unknown' }}</el-tag></dd></div>
          <div><dt>Host</dt><dd class="mono">{{ currentVuln.host || '—' }}</dd></div>
          <div><dt>Matched</dt><dd class="mono">{{ currentVuln.matched || '—' }}</dd></div>
        </dl>
        <div class="evidence-block">
          <div class="evidence-label">Request</div>
          <pre class="mono evidence-pre">{{ currentVuln.request || '—' }}</pre>
        </div>
        <div class="evidence-block">
          <div class="evidence-label">Response</div>
          <pre class="mono evidence-pre">{{ currentVuln.response || '—' }}</pre>
        </div>
      </aside>
    </transition>
  </div>
</template>

<script setup>
import { computed, onMounted, onUnmounted, reactive, ref, h, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { getTask, taskParts, taskPorts, taskServices, taskVulns, taskWeaks, taskEvents,
         pauseTask, resumeTask, terminateTask, restartTask } from '@/api'
import { ElMessage, ElTag } from 'element-plus'

const route = useRoute()
const router = useRouter()
const taskID = computed(() => route.params.id)

const task = ref(null)
const progress = ref({})
const aliveHosts = ref([])
const parts = ref([])
const ports = ref([])
const services = ref([])
const vulns = ref([])
const weaks = ref([])
const events = ref([])
const tab = ref('parts')
const vulnOpen = ref(false)
const currentVuln = ref(null)
const revealed = reactive({})

let timer = null

const PartStatus = {
  props: ['s'],
  render() {
    if (!this.s) return h('span', { class: 'muted' }, '—')
    const map = { completed: 'success', running: 'warning', pending: 'info', failed: 'danger' }
    return h(ElTag, { size: 'small', type: map[this.s] || 'info', effect: 'light' }, () => this.s)
  }
}

async function refresh() {
  try {
    const id = taskID.value
    const r = await getTask(id)
    task.value = r.task
    progress.value = r.progress || {}
    aliveHosts.value = r.alive_hosts || []
    const [p1, p2, p3, p4, p5, p6] = await Promise.all([
      taskParts(id), taskPorts(id), taskServices(id), taskVulns(id), taskWeaks(id), taskEvents(id)
    ])
    parts.value = p1.items || []
    ports.value = p2.items || []
    services.value = p3.items || []
    vulns.value = p4.items || []
    weaks.value = p5.items || []
    events.value = p6.items || []
  } catch (e) {}
}

async function action(a) {
  try {
    const id = taskID.value
    if (a === 'pause') await pauseTask(id)
    if (a === 'resume') await resumeTask(id)
    if (a === 'terminate') await terminateTask(id)
    if (a === 'restart') {
      const res = await restartTask(id)
      if (res?.new_task_id && String(res.new_task_id) !== String(id)) {
        ElMessage.success(`已创建新任务 #${res.new_task_id}`)
        router.push(`/tasks/${res.new_task_id}`)
        return
      }
    }
    ElMessage.success('已发送指令')
    refresh()
  } catch (e) {}
}

function openVuln(row) { currentVuln.value = row; vulnOpen.value = true }

const aliveHostRows = computed(() => aliveHosts.value.map((host) => ({ host })))

function statusType(s) {
  if (s === 'completed') return 'success'
  if (s === 'running') return 'warning'
  if (s === 'paused' || s === 'restarting') return 'warning'
  if (s === 'failed') return 'danger'
  if (s === 'terminated') return 'info'
  return 'info'
}
function sevType(s) {
  if (s === 'critical' || s === 'high') return 'danger'
  if (s === 'medium') return 'warning'
  if (s === 'low' || s === 'info') return 'info'
  return ''
}
function levelType(l) {
  if (l === 'error') return 'danger'
  if (l === 'warn') return 'warning'
  return 'info'
}
function fmt(t) {
  if (!t) return '—'
  const d = new Date(t)
  if (isNaN(d.getTime())) return t
  return d.toLocaleString('zh-CN', { hour12: false, timeZone: 'Asia/Shanghai' })
}

onMounted(() => { refresh(); timer = setInterval(refresh, 3000) })
watch(taskID, refresh)
onUnmounted(() => clearInterval(timer))
</script>

<style scoped>
.evidence-card {
  position: fixed;
  top: 88px;
  right: 24px;
  z-index: 40;
  width: min(560px, calc(100vw - 32px));
  max-height: calc(100vh - 112px);
  overflow: auto;
  background: #ffffff;
  border: 1px solid var(--border);
  border-radius: 8px;
  box-shadow: 0 18px 50px rgba(15, 23, 42, 0.18);
  padding: 16px;
}

.evidence-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  border-bottom: 1px solid var(--border);
  padding-bottom: 12px;
  margin-bottom: 12px;
}

.evidence-title {
  font-size: 15px;
  font-weight: 700;
  color: var(--text);
}

.evidence-sub {
  margin-top: 4px;
  font-size: 12px;
  color: var(--text-muted);
  word-break: break-all;
}

.evidence-meta {
  display: grid;
  grid-template-columns: 1fr;
  gap: 8px;
  margin: 0 0 14px;
}

.evidence-meta div {
  display: grid;
  grid-template-columns: 88px 1fr;
  gap: 10px;
  align-items: start;
}

.evidence-meta dt {
  color: var(--text-muted);
  font-size: 12px;
}

.evidence-meta dd {
  margin: 0;
  font-size: 12.5px;
  word-break: break-all;
}

.evidence-block + .evidence-block {
  margin-top: 14px;
}

.evidence-label {
  font-size: 12px;
  font-weight: 700;
  color: var(--text);
  margin-bottom: 6px;
}

.evidence-pre {
  margin: 0;
  background: #f8fafc;
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 10px 12px;
  max-height: 220px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-word;
  font-size: 12px;
  line-height: 1.55;
}

.evidence-slide-enter-active,
.evidence-slide-leave-active {
  transition: opacity 160ms ease, transform 160ms ease;
}

.evidence-slide-enter-from,
.evidence-slide-leave-to {
  opacity: 0;
  transform: translateX(18px);
}

@media (max-width: 720px) {
  .evidence-card {
    top: 72px;
    right: 16px;
    left: 16px;
    width: auto;
  }
}
</style>
