<script setup>
import {computed, onMounted, onUnmounted, reactive, ref} from 'vue'
import {
  ExportPollutionExcel,
  ImportTXT,
  StartDetection,
} from './api/boce'
import {EventsOn} from '../wailsjs/runtime/runtime'

const rows = ref([])
const selectedId = ref(null)
const running = ref(false)
const paused = ref(false)
const notice = ref('')
const exportPath = ref('')
const progress = ref(0)
const streamTotal = ref(0)
const importedTargets = ref([])
const targetText = ref('')
const targetError = ref('')
const statusFilter = ref('all')

const form = reactive({
  apiKey: '',
  detectionType: 'pollution',
  concurrency: 10,
  timeoutSeconds: 60,
})

const summary = reactive({
  total: 0,
  checked: 0,
  pollution: 0,
  normal: 0,
  unregistered: 0,
  failed: 0,
})

const canPause = computed(() => running.value && rows.value.length > 0)
const statusOrder = ['污染', '拦截', '未备案', '黑名单', '被墙', '疑似被墙', '域名格式错误', '正常', '已备案', '未注册', '失败', '未知']
const statusOptions = computed(() => {
  const statuses = [...new Set(rows.value.map(row => row.status).filter(Boolean))]
  statuses.sort((left, right) => {
    const leftIndex = statusOrder.includes(left) ? statusOrder.indexOf(left) : statusOrder.length
    const rightIndex = statusOrder.includes(right) ? statusOrder.indexOf(right) : statusOrder.length
    if (leftIndex !== rightIndex) {
      return leftIndex - rightIndex
    }
    return left.localeCompare(right, 'zh-Hans-CN')
  })

  const options = [{value: 'all', label: `全部 (${rows.value.length})`}]
  const abnormalCount = rows.value.filter(row => isAbnormalStatus(row.status)).length
  if (abnormalCount > 0) {
    options.push({value: 'abnormal', label: `异常 (${abnormalCount})`})
  }
  statuses.forEach(status => {
    const count = rows.value.filter(row => row.status === status).length
    options.push({value: `status:${status}`, label: `${status} (${count})`})
  })

  return options
})
const displayRows = computed(() => rows.value.filter(row => matchStatusFilter(row.status)))
const displaySummary = computed(() => summarizeRows(displayRows.value))
const isICPResult = computed(() => form.detectionType === 'icp')
const loadingText = computed(() => {
  if (!running.value) {
    return ''
  }
  if (streamTotal.value > 0) {
    return `检测中 ${rows.value.length}/${streamTotal.value}`
  }
  return '检测中...'
})

function isAbnormalStatus(status) {
  return ['污染', '拦截', '未备案', '黑名单', '被墙', '疑似被墙', '域名格式错误', '失败', '未知'].includes(status)
}

function matchStatusFilter(status) {
  if (statusFilter.value === 'abnormal') {
    return isAbnormalStatus(status)
  }
  if (statusFilter.value.startsWith('status:')) {
    return status === statusFilter.value.slice('status:'.length)
  }
  return true
}

function keepValidStatusFilter() {
  if (!statusOptions.value.some(option => option.value === statusFilter.value)) {
    statusFilter.value = 'all'
  }
}

function parseTargetText(text) {
  return parseTargetEntries(text).validTargets
}

function parseTargetEntries(text) {
  const seen = new Set()
  const invalidTargets = []
  const validTargets = []

  text
    .split(/[\s,;，；]+/)
    .map(item => item.trim().replace(/^['"]|['"]$/g, ''))
    .filter(Boolean)
    .forEach(item => {
      const normalized = normalizeTarget(item)
      if (!normalized) {
        invalidTargets.push(item)
        return
      }
      if (!seen.has(normalized)) {
        seen.add(normalized)
        validTargets.push(normalized)
      }
    })

  return {validTargets, invalidTargets}
}

function normalizeTarget(input) {
  const raw = input.trim()
  if (!raw || /[\s@]/.test(raw)) {
    return ''
  }

  let targetURL
  try {
    if (/^https?:\/\//i.test(raw)) {
      targetURL = new URL(raw)
    } else if (/^[a-z][a-z\d+.-]*:\/\//i.test(raw)) {
      return ''
    } else {
      targetURL = new URL(`http://${raw.replace(/^\/+|\/+$/g, '')}`)
    }
  } catch {
    return ''
  }

  const host = targetURL.hostname.toLowerCase()
  if (!isValidHost(host)) {
    return ''
  }
  return host
}

function isValidHost(host) {
  if (isValidIPv4(host)) {
    return true
  }
  if (host.length > 253 || !host.includes('.') || host.includes('..')) {
    return false
  }

  const labels = host.split('.')
  const topLabel = labels.at(-1)
  return labels.every(label => (
    label.length > 0 &&
    label.length <= 63 &&
    /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/i.test(label)
  )) && topLabel.length >= 2 && /[a-z]/i.test(topLabel)
}

function isValidIPv4(host) {
  const parts = host.split('.')
  return parts.length === 4 && parts.every(part => {
    if (!/^\d{1,3}$/.test(part)) {
      return false
    }
    const value = Number(part)
    return value >= 0 && value <= 255 && String(value) === part
  })
}

function validateTargetText() {
  const result = parseTargetEntries(targetText.value)
  if (result.invalidTargets.length) {
    const examples = result.invalidTargets.slice(0, 3).join('、')
    const rest = result.invalidTargets.length > 3 ? ` 等 ${result.invalidTargets.length} 条` : ''
    return {
      ok: false,
      targets: result.validTargets,
      message: `检测目标格式错误: ${examples}${rest}`,
    }
  }
  if (targetText.value.trim() && result.validTargets.length === 0) {
    return {
      ok: false,
      targets: [],
      message: '未解析到有效域名或 URL',
    }
  }
  return {
    ok: true,
    targets: result.validTargets,
    message: '',
  }
}

function summarizeRows(targetRows) {
  return targetRows.reduce((nextSummary, row) => {
    nextSummary.total += 1
    nextSummary.checked += 1
    if (row.status === '污染' || row.status === '拦截' || row.status === '未备案' || row.status === '黑名单' || row.status === '被墙' || row.status === '疑似被墙' || row.status === '域名格式错误') {
      nextSummary.pollution += 1
    } else if (row.status === '正常' || row.status === '已备案') {
      nextSummary.normal += 1
    } else if (row.status === '未注册') {
      nextSummary.unregistered += 1
    } else if (row.status === '失败') {
      nextSummary.failed += 1
    }
    return nextSummary
  }, {
    total: 0,
    checked: 0,
    pollution: 0,
    normal: 0,
    unregistered: 0,
    failed: 0,
  })
}

function applySummary(nextSummary) {
  Object.assign(summary, nextSummary)
}

function upsertRow(row) {
  if (!row || !row.id) {
    return
  }

  const existingIndex = rows.value.findIndex(item => item.id === row.id)
  if (existingIndex >= 0) {
    rows.value.splice(existingIndex, 1, row)
  } else {
    rows.value.push(row)
    rows.value.sort((left, right) => left.id - right.id)
  }
  if (!selectedId.value) {
    selectedId.value = row.id
  }
  if (streamTotal.value > 0) {
    progress.value = Math.min(100, Math.round((rows.value.length / streamTotal.value) * 100))
  }
  keepValidStatusFilter()
}

let unlistenDetectStart = null
let unlistenDetectRow = null

onMounted(() => {
  if (!window.runtime?.EventsOnMultiple) {
    return
  }

  unlistenDetectStart = EventsOn('detect:start', payload => {
    streamTotal.value = Number(payload?.total || 0)
    progress.value = 0
    rows.value = []
    selectedId.value = null
    keepValidStatusFilter()
  })
  unlistenDetectRow = EventsOn('detect:row', row => {
    upsertRow(row)
  })
})

onUnmounted(() => {
  if (unlistenDetectStart) {
    unlistenDetectStart()
  }
  if (unlistenDetectRow) {
    unlistenDetectRow()
  }
})

async function importTxt() {
  const result = await ImportTXT()
  if (!result.canceled && result.targets?.length) {
    importedTargets.value = result.targets
    targetText.value = result.targets.join('\n')
  }
  notice.value = result.message || '已导入测试目标'
}

async function startDetection() {
  const validation = validateTargetText()
  targetError.value = validation.message
  if (!validation.ok) {
    notice.value = validation.message
    return
  }

  running.value = true
  paused.value = false
  notice.value = '正在检测...'
  progress.value = 0
  streamTotal.value = 0
  rows.value = []
  selectedId.value = null
  const targets = validation.targets
  importedTargets.value = targets

  const result = await StartDetection({
    apiKey: form.apiKey,
    enablePollution: form.detectionType === 'pollution',
    enableHijack: form.detectionType === 'qq',
    enableWechat: form.detectionType === 'wechat',
    enableIcp: form.detectionType === 'icp',
    enableBlacklist: form.detectionType === 'blacklist',
    enableWall: form.detectionType === 'wall',
    concurrency: Number(form.concurrency),
    timeoutSeconds: Number(form.timeoutSeconds),
    importedTargetCount: targets.length,
    targets,
  })

  rows.value = result.rows || rows.value
  keepValidStatusFilter()
  selectedId.value = rows.value[12]?.id || rows.value[0]?.id || null
  applySummary(result.summary)
  progress.value = result.progress || 100
  exportPath.value = result.exportPath || exportPath.value
  notice.value = result.message || '测试检测完成'
  running.value = false
}

function pauseDetection() {
  if (!canPause.value) {
    return
  }
  paused.value = !paused.value
  notice.value = paused.value ? '已暂停测试检测' : '继续测试检测'
}

function stopDetection() {
  running.value = false
  paused.value = false
  notice.value = rows.value.length ? '已停止测试检测' : '当前没有运行中的检测任务'
}

async function exportExcel() {
  const result = await ExportPollutionExcel(displayRows.value)
  exportPath.value = result.path || ''
  notice.value = result.message || '导出完成'
}
</script>

<template>
  <main class="tool-window">
    <section class="toolbar toolbar-top" aria-label="API 设置">
      <label class="api-label" for="api-key">API Key</label>
      <input id="api-key" v-model="form.apiKey" class="api-input" type="password" autocomplete="off">
      <button class="win-button import-button" type="button" @click="importTxt">导入TXT</button>
    </section>

    <section class="toolbar options-bar" aria-label="检测设置">
      <span class="field-title">类型</span>
      <label class="check-field">
        <input v-model="form.detectionType" type="radio" name="detection-type" value="pollution">
        <span>污染检测</span>
      </label>
      <label class="check-field">
        <input v-model="form.detectionType" type="radio" name="detection-type" value="qq">
        <span>QQ拦截检测</span>
      </label>
      <label class="check-field">
        <input v-model="form.detectionType" type="radio" name="detection-type" value="wechat">
        <span>微信拦截检测</span>
      </label>
      <label class="check-field">
        <input v-model="form.detectionType" type="radio" name="detection-type" value="icp">
        <span>备案查询</span>
      </label>
      <label class="check-field">
        <input v-model="form.detectionType" type="radio" name="detection-type" value="blacklist">
        <span>备案黑名单检测</span>
      </label>
      <label class="check-field">
        <input v-model="form.detectionType" type="radio" name="detection-type" value="wall">
        <span>被墙检测</span>
      </label>

      <label class="spin-field" for="concurrency">并发</label>
      <input id="concurrency" v-model.number="form.concurrency" class="spin-input" type="number" min="1" max="500">

      <label class="spin-field" for="timeout">超时(秒)</label>
      <input id="timeout" v-model.number="form.timeoutSeconds" class="spin-input" type="number" min="1" max="3600">

      <label class="spin-field" for="status-filter">状态</label>
      <select id="status-filter" v-model="statusFilter" class="select-input">
        <option v-for="option in statusOptions" :key="option.value" :value="option.value">
          {{ option.label }}
        </option>
      </select>

      <button class="win-button action-button start-button" type="button" :disabled="running" @click="startDetection">
        <span v-if="running" class="button-spinner" aria-hidden="true"></span>
        <span>{{ running ? '检测中' : '开始检测' }}</span>
      </button>
      <button class="win-button action-button" type="button" :disabled="!canPause" @click="pauseDetection">
        {{ paused ? '继续' : '暂停' }}
      </button>
      <button class="win-button action-button" type="button" :disabled="!running && !rows.length" @click="stopDetection">停止</button>
      <button class="win-button export-button" type="button" :disabled="!displayRows.length" @click="exportExcel">导出Excel</button>
    </section>

    <section class="target-panel" aria-label="检测目标">
      <label class="target-label" for="target-text">检测目标</label>
      <div class="target-input-wrap">
        <textarea
          id="target-text"
          v-model="targetText"
          class="target-textarea"
          :class="{'target-textarea-error': targetError}"
          spellcheck="false"
          @input="targetError = ''"
        ></textarea>
        <div v-if="targetError" class="target-error">{{ targetError }}</div>
      </div>
    </section>

    <section class="table-frame" :class="{'table-loading': running}" aria-label="检测结果">
      <div v-if="running" class="loading-strip" aria-live="polite">
        <span class="loading-spinner" aria-hidden="true"></span>
        <span>{{ loadingText }}</span>
      </div>
      <table class="result-table">
        <thead v-if="isICPResult">
          <tr>
            <th class="col-target">域名</th>
            <th class="col-status">是否备案</th>
            <th class="col-time">备案号</th>
            <th class="col-error">网站名称</th>
          </tr>
        </thead>
        <thead v-else>
          <tr>
            <th class="col-type">类型</th>
            <th class="col-target">检测目标</th>
            <th class="col-status">状态</th>
            <th class="col-time">检测时间</th>
            <th class="col-error">错误 / 备注</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="row in displayRows"
            :key="row.id"
            :class="{selected: row.id === selectedId}"
            @click="selectedId = row.id"
          >
            <template v-if="isICPResult">
              <td class="col-target">{{ row.domain || row.target }}</td>
              <td class="col-status" :class="{'status-abnormal': isAbnormalStatus(row.status)}">{{ row.status }}</td>
              <td class="col-time">{{ row.beianCode }}</td>
              <td class="col-error">{{ row.siteName }}</td>
            </template>
            <template v-else>
              <td class="col-type">{{ row.type }}</td>
              <td class="col-target">{{ row.target }}</td>
              <td class="col-status" :class="{'status-abnormal': isAbnormalStatus(row.status)}">{{ row.status }}</td>
              <td class="col-time">{{ row.checkedAt }}</td>
              <td class="col-error">{{ row.errorRemark }}</td>
            </template>
          </tr>
        </tbody>
      </table>
      <div v-if="running && !rows.length" class="loading-empty">
        <span class="loading-spinner large" aria-hidden="true"></span>
        <span>正在等待接口返回...</span>
      </div>
    </section>

    <footer class="status-panel">
      <div class="progress-shell" aria-label="检测进度">
        <div class="progress-bar" :style="{width: `${progress}%`}"></div>
      </div>
      <div class="summary-line">
        总数 {{ displaySummary.total }} | 已检测 {{ displaySummary.checked }} |
        <span class="summary-abnormal">异常 {{ displaySummary.pollution }}</span>
        | 正常 {{ displaySummary.normal }} | 未注册 {{ displaySummary.unregistered }} | 失败 {{ displaySummary.failed }}
      </div>
      <div class="export-line">
        <span>导出: {{ exportPath || '等待导出' }}</span>
        <span v-if="notice" class="notice">{{ notice }}</span>
      </div>
    </footer>
  </main>
</template>
