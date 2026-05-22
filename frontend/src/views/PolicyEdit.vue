<template>
  <div v-if="form">
    <div class="page-title">
      <div>
        {{ isNew ? '新建策略' : `策略 #${id}` }}
        <span class="subtitle">{{ form.name }}</span>
      </div>
      <div class="actions">
        <el-button @click="$router.push('/policies')">返回列表</el-button>
        <el-button @click="onPreview">完整配置预览</el-button>
        <el-button type="success" @click="onSaveAndEnable">保存并启用</el-button>
        <el-button type="primary" @click="onSave">保存</el-button>
      </div>
    </div>

    <div class="section-card">
      <div class="card-head">
        <div>
          <div class="card-title">基础信息</div>
          <div class="card-sub">策略名 + 调度层连接参数(写入每个模块的 ConfigMap)</div>
        </div>
      </div>
      <el-form :model="form" label-width="120px" inline>
        <el-form-item label="策略名称">
          <el-input v-model="form.name" style="width:240px" />
        </el-form-item>
        <el-form-item label="K3s Namespace">
          <el-input v-model="form.namespace" style="width:180px" />
        </el-form-item>
        <el-form-item label="调度层内网 IP">
          <el-input v-model="form.scheduler.internal_ip" style="width:200px" />
        </el-form-item>
        <el-form-item label="Redis 端口">
          <el-input-number v-model="form.scheduler.redis_port" :min="1" :max="65535" />
        </el-form-item>
        <el-form-item label="MySQL 端口">
          <el-input-number v-model="form.scheduler.mysql_port" :min="1" :max="65535" />
        </el-form-item>
        <el-form-item label="目标批量大小">
          <el-input-number v-model="form.runtime.target_batch_size" :min="1" :max="10" />
        </el-form-item>
      </el-form>
    </div>

    <div class="section-card">
      <div class="card-head">
        <div>
          <div class="card-title">扫描模块</div>
          <div class="card-sub">
            点击卡片查看/编辑该模块的 JSON 配置。卡片右上角开关控制是否启用(也可在 JSON 里改 <code class="mono">enabled</code> 字段)。
            依赖:nmap 需要 portscan;nuclei/weakpass 需要 portscan + nmap。
          </div>
        </div>
      </div>

      <div class="module-row">
        <div v-for="key in moduleOrder" :key="key" class="module-card"
             :class="{ active: selectedModule === key, off: !form.modules[key].enabled }"
             @click="selectModule(key)">
          <div class="card-head-row">
            <span class="card-title-text">
              <span class="status-dot" :class="form.modules[key].enabled ? 'green' : 'gray'"></span>
              {{ moduleLabel[key] }}
            </span>
            <el-switch v-model="form.modules[key].enabled" @click.stop @change="onEnableToggle(key)" />
          </div>
          <div class="meta">
            <span class="meta-strong">image</span> · {{ shortImage(form.modules[key].image) }}
          </div>
          <div class="meta">
            <span class="meta-strong">replicas</span> · {{ form.modules[key].replicas }}
            &nbsp;·&nbsp;
            <span class="meta-strong">qps</span> · {{ form.modules[key].qps }}
          </div>
        </div>
      </div>

      <div class="module-detail">
        <div class="detail-title">
          <span class="status-dot" :class="form.modules[selectedModule].enabled ? 'green' : 'gray'"></span>
          {{ moduleLabel[selectedModule] }} · JSON 配置
          <div style="margin-left:auto; display:flex; gap:8px;">
            <el-button size="small" plain @click="formatJson">格式化</el-button>
            <el-button size="small" plain @click="resetModule">恢复默认</el-button>
            <el-button size="small" type="primary" plain @click="applyJson">应用</el-button>
          </div>
        </div>

        <div class="json-fields-hint">
          <strong>{{ moduleLabel[selectedModule] }}</strong> 模块字段说明:
          <span v-if="selectedModule === 'portscan'">
            <code class="mono">enabled</code> · <code class="mono">image</code> · <code class="mono">replicas</code> · <code class="mono">ports</code> (字符串数组,支持 <code class="mono">"1-65535"</code>) · <code class="mono">qps</code>
          </span>
          <span v-else-if="selectedModule === 'nmap'">
            <code class="mono">enabled</code> · <code class="mono">image</code> · <code class="mono">replicas</code> · <code class="mono">qps</code> (映射 nmap --max-rate)
          </span>
          <span v-else-if="selectedModule === 'nuclei'">
            <code class="mono">enabled</code> · <code class="mono">image</code> · <code class="mono">replicas</code> · <code class="mono">qps</code> · <code class="mono">template_ids</code> (字符串数组,空表示全量扫描)
          </span>
          <span v-else-if="selectedModule === 'weakpass'">
            <code class="mono">enabled</code> · <code class="mono">image</code> · <code class="mono">replicas</code> · <code class="mono">qps</code> · <code class="mono">dictionary</code> (按 ssh/mysql/redis 划分,Redis 只用 password,username 保持 <code class="mono">[""]</code>)
          </span>
        </div>

        <textarea
          ref="editorRef"
          class="json-editor"
          v-model="moduleJson"
          @blur="applyJson"
          spellcheck="false"
          rows="20"
        />
        <div v-if="jsonError" class="json-error">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" style="width:14px; height:14px;"><circle cx="12" cy="12" r="10"/><path d="M12 8v4M12 16h.01"/></svg>
          {{ jsonError }}
        </div>
      </div>
    </div>

    <el-drawer v-model="previewOpen" title="完整策略 JSON" size="55%">
      <pre class="mono json-preview">{{ previewText }}</pre>
    </el-drawer>
  </div>
</template>

<script setup>
import { onMounted, ref, computed, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { getPolicyTemplate, getPolicy, createPolicy, updatePolicy, enablePolicy } from '@/api'
import { ElMessage } from 'element-plus'

const route = useRoute()
const router = useRouter()
const id = computed(() => route.params.id)
const isNew = computed(() => !id.value)

const form = ref(null)
const defaultTemplate = ref(null) // 用于"恢复默认"
const selectedModule = ref('portscan')
const moduleJson = ref('')
const jsonError = ref('')
const previewOpen = ref(false)
const previewText = ref('')
const editorRef = ref(null)

const moduleOrder = ['portscan', 'nmap', 'nuclei', 'weakpass']
const moduleLabel = { portscan: '端口扫描', nmap: '服务识别', nuclei: '漏洞扫描', weakpass: '弱口令扫描' }

function shortImage(img) {
  if (!img) return '未填写'
  return img.length > 40 ? img.slice(0, 40) + '…' : img
}

function syncJsonFromForm() {
  if (!form.value) return
  normalizeModule(selectedModule.value, form.value.modules[selectedModule.value])
  moduleJson.value = JSON.stringify(form.value.modules[selectedModule.value], null, 2)
  jsonError.value = ''
}

function selectModule(key) {
  if (selectedModule.value === key) return
  // 切换前 apply 当前 JSON;失败时给个 warning 但仍允许切换(避免卡死)
  if (!applyJson({ silent: true })) {
    ElMessage.warning(`${moduleLabel[selectedModule.value]} 的 JSON 有错,刚才的编辑未生效`)
  }
  selectedModule.value = key
}

function applyJson({ silent = false } = {}) {
  if (!form.value) return false
  try {
    const parsed = JSON.parse(moduleJson.value)
    if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
      throw new Error('模块配置必须是一个 JSON 对象')
    }
    normalizeModule(selectedModule.value, parsed)
    form.value.modules[selectedModule.value] = parsed
    jsonError.value = ''
    if (!silent) ElMessage.success('已应用')
    return true
  } catch (e) {
    jsonError.value = e.message
    if (!silent) ElMessage.error('JSON 格式错误,无法应用')
    return false
  }
}

function formatJson() {
  try {
    const parsed = JSON.parse(moduleJson.value)
    moduleJson.value = JSON.stringify(parsed, null, 2)
    jsonError.value = ''
  } catch (e) {
    jsonError.value = e.message
  }
}

function resetModule() {
  if (!defaultTemplate.value) return ElMessage.warning('默认模板未加载')
  const key = selectedModule.value
  form.value.modules[key] = JSON.parse(JSON.stringify(defaultTemplate.value.modules[key]))
  syncJsonFromForm()
  ElMessage.success(`已恢复 ${moduleLabel[key]} 默认配置`)
}

function onEnableToggle(key) {
  // 当前选中的模块卡片开关变化,重新刷新右侧 JSON 显示
  if (key === selectedModule.value) syncJsonFromForm()
}

function normalizePolicy(p) {
  if (!p?.modules) return p
  for (const key of moduleOrder) normalizeModule(key, p.modules[key])
  return p
}

function normalizeModule(key, mod) {
  if (!mod) return
  if (key === 'nuclei' && !Array.isArray(mod.template_ids)) {
    mod.template_ids = []
  }
}

watch(selectedModule, syncJsonFromForm)

onMounted(async () => {
  defaultTemplate.value = normalizePolicy(await getPolicyTemplate())
  if (isNew.value) {
    form.value = JSON.parse(JSON.stringify(defaultTemplate.value))
  } else {
    const r = await getPolicy(id.value)
    form.value = normalizePolicy(r.policy)
  }
  syncJsonFromForm()
})

async function onSave({ stay = false } = {}) {
  // 保存前确保当前编辑中的 JSON 已应用
  if (!applyJson({ silent: true })) {
    ElMessage.error(`${moduleLabel[selectedModule.value]} 的 JSON 有错,请先修正`)
    return null
  }
  normalizePolicy(form.value)
  const payload = JSON.parse(JSON.stringify(form.value))
  if (isNew.value) {
    const r = await createPolicy(payload)
    ElMessage.success('已创建策略')
    if (stay) {
      router.push(`/policies/${r.id}`)
      return r.id
    }
    router.push('/policies')
    return r.id
  }
  await updatePolicy(id.value, payload)
  ElMessage.success('已保存')
  if (!stay) router.push('/policies')
  return id.value
}

async function onSaveAndEnable() {
  const pid = await onSave({ stay: true })
  if (pid) {
    try {
      await enablePolicy(pid)
      ElMessage.success('已启用并部署')
      router.push('/policies')
    } catch (e) {}
  }
}

function onPreview() {
  applyJson({ silent: true })
  normalizePolicy(form.value)
  previewText.value = JSON.stringify(form.value, null, 2)
  previewOpen.value = true
}
</script>

<style scoped>
.json-fields-hint {
  font-size: 12px;
  color: var(--text-muted);
  margin-bottom: 10px;
  padding: 10px 12px;
  background: var(--info-bg);
  border-left: 3px solid var(--info);
  border-radius: 4px;
  line-height: 1.7;
}
.json-fields-hint code {
  background: rgba(15, 23, 42, 0.06);
  padding: 1px 5px;
  border-radius: 3px;
  font-size: 11.5px;
}

.json-editor {
  width: 100%;
  min-height: 360px;
  background: #0f172a;
  color: #e2e8f0;
  border: 1px solid #1e293b;
  border-radius: 8px;
  padding: 14px 16px;
  font-family: 'JetBrains Mono', ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 13px;
  line-height: 1.65;
  outline: none;
  resize: vertical;
  tab-size: 2;
  caret-color: #6366f1;
}
.json-editor:focus {
  border-color: #6366f1;
  box-shadow: 0 0 0 3px rgba(99, 102, 241, 0.18);
}

.json-error {
  margin-top: 10px;
  padding: 8px 12px;
  background: var(--danger-bg);
  color: var(--danger);
  border-radius: 6px;
  font-size: 12.5px;
  display: flex;
  align-items: center;
  gap: 6px;
  font-family: 'JetBrains Mono', ui-monospace, monospace;
}

.json-preview {
  white-space: pre-wrap;
  background: #0f172a;
  color: #e2e8f0;
  padding: 16px;
  border-radius: 8px;
  font-size: 12.5px;
  line-height: 1.65;
  border: 1px solid #1e293b;
  overflow: auto;
}
</style>
