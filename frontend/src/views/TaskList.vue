<template>
  <div>
    <div class="page-title">
      任务管理
      <div class="actions">
        <el-button @click="refresh">刷新</el-button>
        <el-button type="primary" @click="$router.push('/tasks/new')">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" style="width:14px; height:14px; margin-right:6px;"><path d="M12 5v14M5 12h14"/></svg>
          新建任务
        </el-button>
      </div>
    </div>

    <div class="section-card">
      <div class="toolbar">
        <el-select v-model="filter.status" placeholder="全部状态" clearable size="default" style="width:160px" @change="refresh">
          <el-option v-for="s in statuses" :key="s" :label="s" :value="s" />
        </el-select>
        <div class="grow"></div>
        <div class="muted">共 {{ total }} 条</div>
      </div>

      <el-table :data="rows" v-loading="loading" stripe>
        <el-table-column label="ID" width="80">
          <template #default="{ row }"><span class="mono">#{{ row.id }}</span></template>
        </el-table-column>
        <el-table-column prop="name" label="任务名" min-width="180" />
        <el-table-column label="策略" width="100">
          <template #default="{ row }"><span class="mono">#{{ row.policy_id }}</span></template>
        </el-table-column>
        <el-table-column label="状态" width="110">
          <template #default="{ row }">
            <el-tag :type="statusType(row.status)" size="small" effect="light">{{ row.status }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="target_total" label="目标" width="80" />
        <el-table-column prop="alive_total" label="存活" width="80" />
        <el-table-column prop="part_total" label="分片" width="80" />
        <el-table-column label="创建时间" width="180">
          <template #default="{ row }"><span class="mono">{{ fmt(row.created_at) }}</span></template>
        </el-table-column>
        <el-table-column label="操作" width="330" fixed="right">
          <template #default="{ row }">
            <el-button size="small" plain @click="$router.push(`/tasks/${row.id}`)">详情</el-button>
            <el-button size="small" v-if="row.status === 'running'" @click="onAction(row, 'pause')">暂停</el-button>
            <el-button size="small" type="success" v-if="row.status === 'paused'" @click="onAction(row, 'resume')">继续</el-button>
            <el-button size="small" type="danger" plain v-if="['queued','running','paused','restarting'].includes(row.status)" @click="onAction(row, 'terminate')">终止</el-button>
            <el-button size="small" v-if="['completed','terminated','failed'].includes(row.status)" @click="onAction(row, 'restart')">重启</el-button>
            <el-button size="small" type="danger" v-if="['completed','terminated','failed'].includes(row.status)" @click="onDelete(row)">删除</el-button>
          </template>
        </el-table-column>
        <template #empty>
          <div class="empty-state">
            <div class="muted">没有匹配的任务</div>
          </div>
        </template>
      </el-table>

      <el-pagination
        v-model:current-page="filter.page"
        v-model:page-size="filter.page_size"
        :page-sizes="[10, 20, 50]"
        :total="total"
        layout="prev, pager, next, sizes, total"
        style="margin-top:14px; justify-content:flex-end;"
        @current-change="refresh" @size-change="refresh"
      />
    </div>
  </div>
</template>

<script setup>
import { onMounted, reactive, ref } from 'vue'
import { listTasks, pauseTask, resumeTask, terminateTask, restartTask, deleteTask } from '@/api'
import { ElMessage, ElMessageBox } from 'element-plus'

const filter = reactive({ status: '', page: 1, page_size: 10 })
const rows = ref([])
const total = ref(0)
const loading = ref(false)
const statuses = ['queued', 'running', 'paused', 'restarting', 'completed', 'terminated', 'failed']

async function refresh() {
  loading.value = true
  try {
    const r = await listTasks({ status: filter.status, page: filter.page, page_size: filter.page_size })
    rows.value = r.items || []
    total.value = r.total || 0
  } finally {
    loading.value = false
  }
}

async function onAction(row, action) {
  try {
    if (action === 'pause') await pauseTask(row.id)
    if (action === 'resume') await resumeTask(row.id)
    if (action === 'terminate') await terminateTask(row.id)
    if (action === 'restart') {
      const res = await restartTask(row.id)
      if (res?.new_task_id && String(res.new_task_id) !== String(row.id)) {
        ElMessage.success(`已创建新任务 #${res.new_task_id}`)
        refresh()
        return
      }
    }
    ElMessage.success('已发送指令')
    refresh()
  } catch (e) {}
}

async function onDelete(row) {
  try {
    await ElMessageBox.confirm(`删除任务 #${row.id} 后会同步删除该任务的分片、事件和扫描结果。`, '删除任务', {
      confirmButtonText: '删除',
      cancelButtonText: '取消',
      type: 'warning',
      confirmButtonClass: 'el-button--danger'
    })
    await deleteTask(row.id)
    ElMessage.success('已删除任务')
    refresh()
  } catch (e) {}
}

function statusType(s) {
  if (s === 'running' || s === 'completed') return 'success'
  if (s === 'paused' || s === 'restarting') return 'warning'
  if (s === 'failed') return 'danger'
  if (s === 'terminated') return 'info'
  return ''
}

function fmt(t) {
  if (!t) return '—'
  const d = new Date(t)
  if (isNaN(d.getTime())) return t
  return d.toLocaleString('zh-CN', { hour12: false, timeZone: 'Asia/Shanghai' })
}

onMounted(refresh)
</script>
