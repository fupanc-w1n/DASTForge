<template>
  <div>
    <div class="page-title">
      新建任务
      <div class="actions">
        <el-button @click="$router.push('/tasks')">返回</el-button>
      </div>
    </div>

    <div class="section-card" style="max-width:760px">
      <div class="card-head">
        <div>
          <div class="card-title">任务参数</div>
          <div class="card-sub">提交后会自动测活、分片、按策略下发到端口扫描 Stream。</div>
        </div>
      </div>
      <el-form :model="form" label-width="120px">
        <el-form-item label="任务名称">
          <el-input v-model="form.name" placeholder="例如:官网定期扫描" style="width:360px" />
        </el-form-item>
        <el-form-item label="扫描策略">
          <el-select v-model="form.policy_id" placeholder="请选择已启用的策略" style="width:360px">
            <el-option v-for="p in policies" :key="p.id"
                       :label="`#${p.id} · ${p.name}`"
                       :value="p.id"
                       :disabled="!p.enabled">
              <span style="float:left">#{{ p.id }} {{ p.name }}</span>
              <span style="float:right; font-size:11px;"
                    :style="{ color: p.enabled ? 'var(--success)' : 'var(--text-muted)' }">
                {{ p.enabled ? '已启用' : '未启用' }}
              </span>
            </el-option>
          </el-select>
          <div class="muted" style="margin-top:4px">只有已启用的策略可以选择(后端会在启用时部署 Pod)</div>
        </el-form-item>
        <el-form-item label="目标列表">
          <el-input type="textarea" v-model="form.targetsText" :rows="10"
                    placeholder="每行一个 host / IP / domain,不支持 host:port 形式&#10;例:&#10;example.com&#10;192.168.1.10&#10;10.0.0.5"
                    style="width:480px; font-family: 'JetBrains Mono', monospace;" />
          <div class="muted" style="margin-top:4px">
            支持 host / IP / domain,每行一个;后端会做主机测活,丢弃不可达目标,并按 ≤10 个/批切分。
          </div>
        </el-form-item>
        <el-form-item>
          <el-button type="primary" @click="onSubmit" :loading="submitting">提交任务</el-button>
          <el-button @click="$router.push('/tasks')">取消</el-button>
        </el-form-item>
      </el-form>
    </div>
  </div>
</template>

<script setup>
import { onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { listPolicies, createTask } from '@/api'
import { ElMessage } from 'element-plus'

const router = useRouter()
const policies = ref([])
const submitting = ref(false)
const form = reactive({ name: '', policy_id: null, targetsText: '' })

onMounted(async () => {
  const r = await listPolicies()
  policies.value = r.items || []
})

async function onSubmit() {
  if (!form.name) return ElMessage.warning('请输入任务名称')
  if (!form.policy_id) return ElMessage.warning('请选择策略')
  const targets = form.targetsText.split(/[\s,;]+/).map((s) => s.trim()).filter(Boolean)
  if (targets.length === 0) return ElMessage.warning('请输入目标')
  submitting.value = true
  try {
    const r = await createTask({ name: form.name, policy_id: form.policy_id, targets })
    ElMessage.success(`已创建任务 #${r.id}`)
    router.push(`/tasks/${r.id}`)
  } finally {
    submitting.value = false
  }
}
</script>
