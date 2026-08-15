const messages = {
  zh: {
    loadingMetrics: '正在读取指标...', queueOverview: '队列概览', statusHint: '点击状态卡筛选；再点一次取消',
    queued: '排队中', retrying: '等待重试', running: '执行中', succeeded: '成功', partiallySucceeded: '部分成功',
    failed: '失败', canceled: '已取消', runtimeSignals: '运行信号', processCounters: '当前进程计数，重启后归零',
    oldestAvailable: '最早可领取任务', claimAttempts: '领取尝试', claimMisses: '空领取', tasksClaimed: '成功领取',
    inspectTask: '任务详情', taskLookup: '按任务 ID 查询，或点击下方列表', inspect: '查询',
    inspectEmpty: '输入任务 ID，或从下方列表点选任务。', taskTimeline: '任务列表', allStatuses: '全部状态',
    workflowPlaceholder: 'Workflow（精确匹配）', apply: '应用', clear: '清除', taskId: '任务 ID', status: '状态',
    progress: '进度', retries: '重试', createdAt: '创建时间', error: '错误', loadingTasks: '正在加载任务...',
    listHint: '每页最多 25 条，按创建时间倒序', previous: '上一页', next: '下一页', updated: '更新于',
    metricsUnavailable: '指标不可用', tasksLoaded: '本页 {count} 条', taskListUnavailable: '任务列表不可用',
    noTasks: '没有符合条件的任务', loadingTask: '正在加载任务...', lookupFailed: '任务查询失败',
    noEvents: '没有事件记录', events: '事件时间线', workflow: 'Workflow', created: '创建时间',
    retryCount: '重试次数', errorLabel: '错误信息', seconds: '秒', minutes: '分', hours: '小时',
    autoRefresh: '自动刷新', refresh: '刷新', scopeAll: '范围：全部任务', scopeWorkflow: 'Workflow：{workflow}',
    scopeStatus: '状态：{status}', runtimeAll: '范围：全部 Workflow · 当前进程',
    runtimeWorkflow: '范围：{workflow} · 当前进程', workerId: 'Worker', leaseUntil: '租约到期',
    lastHeartbeat: '最近心跳', availableAt: '可执行时间', nextRetryAt: '下次重试', queueDuration: '排队耗时',
    executionDuration: '执行耗时', totalDuration: '总耗时', errorCode: '错误分类', retryable: '可重试',
    resultRef: '结果引用', requestedBy: '请求来源', batchId: '批次 ID', inputReference: '输入引用',
    rawJSON: '原始 JSON', cancelTask: '取消任务',
    cancelConfirm: '确定取消任务 {id} 吗？运行中的 Worker 将失去有效租约。', cancelFailed: '取消失败',
    yes: '是', no: '否', none: '-', pagePaused: '页面在后台，自动刷新已暂停', autoOff: '自动刷新已关闭',
    missRatio: '空领占比 {ratio}', sectionIdentity: '身份与归属', sectionTiming: '时间与耗时',
    sectionExecution: '执行与结果', sectionFailure: '失败信息', clearSelection: '清除选中',
    relativeJustNow: '刚刚', relativeSeconds: '{n} 秒前', relativeMinutes: '{n} 分钟前',
    relativeHours: '{n} 小时前', relativeDays: '{n} 天前',
    event_task_created: '任务已创建并进入队列', event_task_started: 'Worker 已领取并开始执行',
    event_task_retry_started: '重试任务已被重新领取', event_task_recovered: '原租约过期，任务已被其他 Worker 恢复',
    event_task_retrying: '执行失败，任务已按退避策略等待重试', event_task_released: 'Worker 优雅退出，任务已主动归还队列',
    event_task_progress: 'Worker 上报执行进度', event_task_succeeded: '任务执行成功',
    event_task_partially_succeeded: '任务部分成功', event_task_failed: '任务执行失败并进入终态',
    event_task_canceled: '任务已被调用方取消', event_item_started: '子项开始执行',
    event_item_succeeded: '子项执行成功', event_item_failed: '子项执行失败', event_item_retrying: '子项等待重试',
    worker: 'Worker', lease: '租约', failureClass: '失败分类', backoff: '退避', recovery: '恢复', rawType: '原始类型'
  },
  en: {
    loadingMetrics: 'Loading metrics...', queueOverview: 'Queue Overview',
    statusHint: 'Click a status card to filter; click again to clear',
    queued: 'Queued', retrying: 'Retrying', running: 'Running', succeeded: 'Succeeded',
    partiallySucceeded: 'Partially succeeded', failed: 'Failed', canceled: 'Canceled',
    runtimeSignals: 'Runtime Signals', processCounters: 'Current process counters; reset on restart',
    oldestAvailable: 'Oldest available task', claimAttempts: 'Claim attempts', claimMisses: 'Claim misses',
    tasksClaimed: 'Tasks claimed', inspectTask: 'Task Detail',
    taskLookup: 'Lookup by task ID, or select from the list', inspect: 'Inspect',
    inspectEmpty: 'Enter a task ID or select a row below.', taskTimeline: 'Task List',
    allStatuses: 'All statuses', workflowPlaceholder: 'Workflow (exact match)', apply: 'Apply', clear: 'Clear',
    taskId: 'Task ID', status: 'Status', progress: 'Progress', retries: 'Retries', createdAt: 'Created at',
    error: 'Error', loadingTasks: 'Loading tasks...', listHint: 'Up to 25 tasks per page, newest first',
    previous: 'Previous', next: 'Next', updated: 'Updated', metricsUnavailable: 'Metrics unavailable',
    tasksLoaded: '{count} on this page', taskListUnavailable: 'Task list unavailable',
    noTasks: 'No tasks match the current filters', loadingTask: 'Loading task...',
    lookupFailed: 'Task lookup failed', noEvents: 'No events recorded', events: 'Event Timeline',
    workflow: 'Workflow', created: 'Created', retryCount: 'Retries', errorLabel: 'Error',
    seconds: 's', minutes: 'm', hours: 'h', autoRefresh: 'Auto refresh', refresh: 'Refresh',
    scopeAll: 'Scope: all tasks', scopeWorkflow: 'Workflow: {workflow}', scopeStatus: 'Status: {status}',
    runtimeAll: 'Scope: all workflows · current process',
    runtimeWorkflow: 'Scope: {workflow} · current process', workerId: 'Worker', leaseUntil: 'Lease until',
    lastHeartbeat: 'Last heartbeat', availableAt: 'Available at', nextRetryAt: 'Next retry',
    queueDuration: 'Queue duration', executionDuration: 'Execution duration', totalDuration: 'Total duration',
    errorCode: 'Error code', retryable: 'Retryable', resultRef: 'Result reference', requestedBy: 'Requested by',
    batchId: 'Batch ID', inputReference: 'Input reference', rawJSON: 'Raw JSON', cancelTask: 'Cancel task',
    cancelConfirm: 'Cancel task {id}? A running Worker will lose its valid lease.', cancelFailed: 'Cancel failed',
    yes: 'Yes', no: 'No', none: '-', pagePaused: 'Page is in the background; auto refresh paused',
    autoOff: 'Auto refresh is off', missRatio: 'Miss ratio {ratio}', sectionIdentity: 'Identity',
    sectionTiming: 'Timing', sectionExecution: 'Execution', sectionFailure: 'Failure',
    clearSelection: 'Clear selection', relativeJustNow: 'just now', relativeSeconds: '{n}s ago',
    relativeMinutes: '{n}m ago', relativeHours: '{n}h ago', relativeDays: '{n}d ago',
    event_task_created: 'Task created and queued', event_task_started: 'Worker claimed the task and started execution',
    event_task_retry_started: 'Retry attempt claimed by a Worker',
    event_task_recovered: 'Expired lease recovered by another Worker',
    event_task_retrying: 'Execution failed; task waiting for retry backoff',
    event_task_released: 'Worker returned the task during graceful shutdown',
    event_task_progress: 'Worker reported execution progress', event_task_succeeded: 'Task execution succeeded',
    event_task_partially_succeeded: 'Task partially succeeded',
    event_task_failed: 'Task failed and reached a terminal state', event_task_canceled: 'Task canceled by caller',
    event_item_started: 'Item execution started', event_item_succeeded: 'Item execution succeeded',
    event_item_failed: 'Item execution failed', event_item_retrying: 'Item waiting for retry',
    worker: 'Worker', lease: 'Lease', failureClass: 'Failure class', backoff: 'Backoff', recovery: 'Recovery',
    rawType: 'Raw type'
  }
};

const statusKeys = {
  queued: 'queued', retrying: 'retrying', running: 'running', succeeded: 'succeeded',
  partially_succeeded: 'partiallySucceeded', failed: 'failed', canceled: 'canceled'
};
const terminalStatuses = new Set(['succeeded', 'partially_succeeded', 'failed', 'canceled']);
const cancelableStatuses = new Set(['queued', 'retrying', 'running']);
const listState = { cursor: '', nextCursor: '', history: [], hasMore: false };
const filters = { status: '', workflow: '' };
let locale = localStorage.getItem('taskpulse.locale') || (navigator.language.startsWith('zh') ? 'zh' : 'en');
let numberFormat = new Intl.NumberFormat(locale === 'zh' ? 'zh-CN' : 'en-US');
let selectedTaskID = '';
let selectedTaskStatus = '';
let lastDetailRefreshAt = 0;
let detailRequestToken = 0;

const t = key => messages[locale][key] || key;
const escapeHTML = value => String(value ?? '').replace(/[&<>'"]/g, char => ({
  '&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&#39;', '"': '&quot;'
}[char]));
const safeJSON = value => JSON.parse(JSON.stringify(value, (key, item) => key === 'lease_token' ? undefined : item));

function statusTone(status) {
  if (status === 'partially_succeeded') return 'partial';
  return statusKeys[status] ? status : 'canceled';
}

function applyLocale() {
  document.documentElement.lang = locale === 'zh' ? 'zh-CN' : 'en';
  numberFormat = new Intl.NumberFormat(locale === 'zh' ? 'zh-CN' : 'en-US');
  document.querySelectorAll('[data-i18n]').forEach(node => {
    if (node.id === 'task-detail' && selectedTaskID) return;
    node.textContent = t(node.dataset.i18n);
  });
  document.querySelectorAll('[data-i18n-placeholder]').forEach(node => {
    node.placeholder = t(node.dataset.i18nPlaceholder);
  });
  document.getElementById('lang-zh').classList.toggle('active', locale === 'zh');
  document.getElementById('lang-en').classList.toggle('active', locale === 'en');
  updateScopeLabels();
  refreshAll(true);
}

function metric(text, name, labels) {
  const labelText = Object.entries(labels || {}).map(([key, value]) => `${key}="${value}"`).join(',');
  const prefix = labelText ? `${name}{${labelText}} ` : `${name} `;
  const line = text.split(/\r?\n/).find(value => value.startsWith(prefix));
  return line ? Number(line.slice(prefix.length)) : 0;
}

function workflowMetric(text, name) {
  if (filters.workflow) return metric(text, name, { workflow: filters.workflow });
  return text.split(/\r?\n/)
    .filter(line => line.startsWith(`${name}{`))
    .reduce((total, line) => total + Number(line.slice(line.lastIndexOf(' ') + 1)), 0);
}

function duration(seconds) {
  if (!Number.isFinite(seconds) || seconds < 0) return t('none');
  if (seconds < 60) return `${Math.round(seconds)}${t('seconds')}`;
  if (seconds < 3600) {
    return `${Math.floor(seconds / 60)}${t('minutes')} ${Math.round(seconds % 60)}${t('seconds')}`;
  }
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  return `${hours}${t('hours')} ${minutes}${t('minutes')}`;
}

function durationMS(milliseconds) { return duration(Number(milliseconds || 0) / 1000); }

function dateTime(value) {
  return value ? new Date(value).toLocaleString(locale === 'zh' ? 'zh-CN' : 'en-US') : t('none');
}

function relativeTime(value) {
  if (!value) return t('none');
  const delta = Math.max(0, Date.now() - new Date(value).getTime());
  const seconds = Math.floor(delta / 1000);
  if (seconds < 10) return t('relativeJustNow');
  if (seconds < 60) return t('relativeSeconds').replace('{n}', String(seconds));
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return t('relativeMinutes').replace('{n}', String(minutes));
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return t('relativeHours').replace('{n}', String(hours));
  return t('relativeDays').replace('{n}', String(Math.floor(hours / 24)));
}

function updateScopeLabels() {
  const chips = [];
  if (filters.workflow) chips.push(t('scopeWorkflow').replace('{workflow}', filters.workflow));
  else chips.push(t('scopeAll'));
  if (filters.status) {
    chips.push(t('scopeStatus').replace('{status}', t(statusKeys[filters.status] || filters.status)));
  }
  document.getElementById('stats-scope').innerHTML = chips
    .map(text => `<span class="scope-chip">${escapeHTML(text)}</span>`)
    .join('');

  const runtimeScope = filters.workflow
    ? t('runtimeWorkflow').replace('{workflow}', filters.workflow)
    : t('runtimeAll');
  const runtimeNode = document.getElementById('runtime-scope');
  if (runtimeNode) runtimeNode.textContent = runtimeScope;
}

async function refreshStats() {
  const params = new URLSearchParams();
  if (filters.workflow) params.set('workflow', filters.workflow);
  const response = await fetch(`/task-stats?${params}`, { cache: 'no-store' });
  if (!response.ok) throw new Error(`HTTP ${response.status}`);
  const stats = await response.json();
  Object.keys(statusKeys).forEach(status => {
    document.getElementById(status).textContent = numberFormat.format((stats.status_counts || {})[status] || 0);
  });
  const ages = stats.oldest_available_age_seconds || {};
  document.getElementById('oldest').textContent = duration(Math.max(ages.queued || 0, ages.retrying || 0));
}

async function refreshRuntimeMetrics() {
  const response = await fetch('/metrics', { cache: 'no-store' });
  if (!response.ok) throw new Error(`HTTP ${response.status}`);
  const text = await response.text();
  const attempts = workflowMetric(text, 'taskpulse_claim_attempts_total');
  const misses = workflowMetric(text, 'taskpulse_claim_misses_total');
  const claimed = workflowMetric(text, 'taskpulse_tasks_claimed_total');
  document.getElementById('attempts').textContent = numberFormat.format(attempts);
  document.getElementById('misses').textContent = numberFormat.format(misses);
  document.getElementById('claimed').textContent = numberFormat.format(claimed);
  const ratioNode = document.getElementById('miss-ratio');
  if (ratioNode) {
    if (attempts > 0) {
      const ratio = `${((misses / attempts) * 100).toFixed(1)}%`;
      ratioNode.textContent = t('missRatio').replace('{ratio}', ratio);
    } else {
      ratioNode.textContent = '';
    }
  }
}

function resetPagination() {
  listState.cursor = '';
  listState.nextCursor = '';
  listState.history = [];
  listState.hasMore = false;
}

function progressMarkup(task) {
  const progress = Math.max(0, Math.min(100, Number(task.progress) || 0));
  let fillClass = '';
  if (task.status === 'failed' || task.status === 'canceled') fillClass = 'is-failed';
  else if (terminalStatuses.has(task.status) || progress >= 100) fillClass = 'is-done';
  return `<span class="progress-bar" title="${escapeHTML(progress)}%"><span>${escapeHTML(progress)}%</span><span class="progress-track"><span class="progress-fill ${fillClass}" style="width:${progress}%"></span></span></span>`;
}

async function loadTasks(showLoading) {
  const rows = document.getElementById('task-rows');
  if (showLoading) {
    rows.innerHTML = `<tr><td colspan="7" class="empty">${escapeHTML(t('loadingTasks'))}</td></tr>`;
  }
  const params = new URLSearchParams({ limit: '25' });
  if (filters.status) params.set('status', filters.status);
  if (filters.workflow) params.set('workflow', filters.workflow);
  if (listState.cursor) params.set('cursor', listState.cursor);
  try {
    const response = await fetch(`/tasks?${params}`, { cache: 'no-store' });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const page = await response.json();
    listState.nextCursor = page.next_cursor || '';
    listState.hasMore = Boolean(page.has_more);
    renderTasks(page.items || []);
    document.getElementById('list-status').textContent = t('tasksLoaded')
      .replace('{count}', numberFormat.format((page.items || []).length));
  } catch (error) {
    rows.innerHTML = `<tr><td colspan="7" class="empty">${escapeHTML(t('taskListUnavailable'))}: ${escapeHTML(error.message)}</td></tr>`;
    document.getElementById('list-status').textContent = '';
  }
  document.getElementById('previous-page').disabled = listState.history.length === 0;
  document.getElementById('next-page').disabled = !listState.hasMore;
}

function renderTasks(items) {
  const rows = document.getElementById('task-rows');
  if (!items.length) {
    rows.innerHTML = `<tr><td colspan="7" class="empty">${escapeHTML(t('noTasks'))}</td></tr>`;
    return;
  }
  rows.innerHTML = items.map(task => {
    const selected = task.id === selectedTaskID ? ' is-selected' : '';
    const tone = statusTone(task.status);
    return `<tr class="${selected}" data-task-id="${escapeHTML(task.id)}" tabindex="0">
      <td class="col-id mono" title="${escapeHTML(task.id)}">${escapeHTML(task.id)}</td>
      <td class="col-workflow" title="${escapeHTML(task.workflow)}">${escapeHTML(task.workflow)}</td>
      <td class="col-status"><span class="badge tone-${escapeHTML(tone)}">${escapeHTML(t(statusKeys[task.status] || task.status))}</span></td>
      <td class="col-progress">${progressMarkup(task)}</td>
      <td class="col-retry">${escapeHTML(task.retry_count)} / ${escapeHTML(task.max_retries)}</td>
      <td class="col-time" title="${escapeHTML(dateTime(task.created_at))}">${escapeHTML(relativeTime(task.created_at))}</td>
      <td class="col-error" title="${escapeHTML(task.error_message || '')}">${escapeHTML(task.error_message || t('none'))}</td>
    </tr>`;
  }).join('');
  rows.querySelectorAll('tr[data-task-id]').forEach(row => {
    const open = () => selectTask(row.dataset.taskId);
    row.addEventListener('click', open);
    row.addEventListener('keydown', event => {
      if (event.key === 'Enter' || event.key === ' ') {
        event.preventDefault();
        open();
      }
    });
  });
}

function selectTask(taskID) {
  if (!taskID) return;
  selectedTaskID = taskID;
  selectedTaskStatus = '';
  document.getElementById('task-id').value = taskID;
  document.querySelectorAll('#task-rows tr[data-task-id]').forEach(row => {
    row.classList.toggle('is-selected', row.dataset.taskId === taskID);
  });
  inspectTask(taskID, true);
  document.getElementById('task-detail').scrollIntoView({ behavior: 'smooth', block: 'nearest' });
}

function clearSelection() {
  selectedTaskID = '';
  selectedTaskStatus = '';
  document.getElementById('task-id').value = '';
  document.querySelectorAll('#task-rows tr.is-selected').forEach(row => row.classList.remove('is-selected'));
  const target = document.getElementById('task-detail');
  target.className = 'empty';
  target.textContent = t('inspectEmpty');
}

function eventTone(type) {
  if (type === 'task_succeeded' || type === 'item_succeeded' || type === 'task_partially_succeeded') return 'good';
  if (type === 'task_failed' || type === 'item_failed' || type === 'task_canceled') return 'bad';
  if (type === 'task_retrying' || type === 'item_retrying' || type === 'task_recovered') return 'warn';
  if (type === 'task_progress' || type === 'task_released') return 'muted';
  return '';
}

function eventDescription(event) {
  let payload = {};
  try {
    payload = event.payload || {};
    if (typeof payload === 'string') payload = JSON.parse(payload);
  } catch (_) {
    payload = {};
  }
  const details = [];
  if (payload.worker_id) details.push(`${t('worker')}: ${payload.worker_id}`);
  if (payload.lease_until) details.push(`${t('lease')}: ${dateTime(payload.lease_until)}`);
  if (payload.error_code) details.push(`${t('failureClass')}: ${payload.error_code}`);
  if (payload.delay_ms) details.push(`${t('backoff')}: ${durationMS(payload.delay_ms)}`);
  if (payload.available_at) details.push(`${t('nextRetryAt')}: ${dateTime(payload.available_at)}`);
  if (event.type === 'task_recovered') details.push(t('recovery'));
  if (event.progress > 0 || event.type === 'task_progress') details.push(`${t('progress')}: ${event.progress}%`);
  return { title: t(`event_${event.type}`), details: details.join(' · ') };
}

function detailRow(label, value) {
  return `<dt>${escapeHTML(label)}</dt><dd>${escapeHTML(value ?? t('none'))}</dd>`;
}

function referenceValue(value) {
  if (value === null || value === undefined || value === '') return t('none');
  return typeof value === 'object' ? JSON.stringify(value, null, 2) : String(value);
}

function referenceFields(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value) || Object.keys(value).length === 0) {
    return `<span class="muted">${escapeHTML(t('none'))}</span>`;
  }
  return `<div class="reference-fields">${Object.entries(value).map(([key, item]) => `
    <div class="reference-field">
      <span class="reference-key">${escapeHTML(key)}</span>
      <span class="reference-value">${escapeHTML(referenceValue(item))}</span>
    </div>`).join('')}</div>`;
}

function detailReferenceRow(label, value) {
  return `<dt>${escapeHTML(label)}</dt><dd>${referenceFields(value)}</dd>`;
}

function detailSection(title, rows) {
  return `<section class="detail-section"><h3>${escapeHTML(title)}</h3><dl>${rows.join('')}</dl></section>`;
}

async function inspectTask(taskID, force) {
  if (!taskID) return;
  const now = Date.now();
  if (!force && terminalStatuses.has(selectedTaskStatus) && now - lastDetailRefreshAt < 30000) return;

  const requestToken = ++detailRequestToken;
  const target = document.getElementById('task-detail');
  const firstLoad = !selectedTaskStatus;
  if (firstLoad) {
    target.className = 'empty';
    target.textContent = t('loadingTask');
  }

  try {
    const [taskResponse, eventsResponse] = await Promise.all([
      fetch(`/tasks/${encodeURIComponent(taskID)}`, { cache: 'no-store' }),
      fetch(`/tasks/${encodeURIComponent(taskID)}/events`, { cache: 'no-store' })
    ]);
    if (requestToken !== detailRequestToken) return;
    if (!taskResponse.ok) throw new Error(`HTTP ${taskResponse.status}`);
    const task = await taskResponse.json();
    const events = eventsResponse.ok ? await eventsResponse.json() : [];
    selectedTaskID = taskID;
    selectedTaskStatus = task.status;
    lastDetailRefreshAt = now;

    const input = task.input && typeof task.input === 'object' ? task.input : {};
    const rawTask = safeJSON(task);
    const tone = statusTone(task.status);
    const statusLabel = t(statusKeys[task.status] || task.status);

    const eventList = events.length
      ? `<ul class="events">${events.map(event => {
        const description = eventDescription(event);
        const toneClass = eventTone(event.type);
        return `<li class="${toneClass ? `is-${toneClass}` : ''}">
          <strong>${escapeHTML(description.title)}</strong>
          <div>${escapeHTML(description.details || event.message || '')}</div>
          <div class="event-meta">${escapeHTML(dateTime(event.created_at))} · ${escapeHTML(relativeTime(event.created_at))} · ${escapeHTML(t('rawType'))}: ${escapeHTML(event.type)}</div>
        </li>`;
      }).join('')}</ul>`
      : `<div class="empty">${escapeHTML(t('noEvents'))}</div>`;

    const cancelButton = cancelableStatuses.has(task.status)
      ? `<button id="cancel-selected-task" type="button" class="danger">${escapeHTML(t('cancelTask'))}</button>`
      : '';
    target.className = 'detail';
    target.innerHTML = `
      <div class="detail-actions">
        <button id="clear-selected-task" type="button" class="secondary">${escapeHTML(t('clearSelection'))}</button>
        ${cancelButton}
      </div>
      <div class="detail-sections">
        ${detailSection(t('sectionIdentity'), [
          `<dt>${escapeHTML(t('status'))}</dt><dd><span class="status-pill tone-${escapeHTML(tone)}">${escapeHTML(statusLabel)}</span></dd>`,
          detailRow(t('workflow'), task.workflow),
          detailRow(t('requestedBy'), input.requested_by || t('none')),
          detailRow(t('batchId'), input.batch_id || t('none')),
          detailRow(t('workerId'), task.worker_id || t('none'))
        ])}
        ${detailSection(t('sectionTiming'), [
          detailRow(t('created'), dateTime(task.created_at)),
          detailRow(t('leaseUntil'), dateTime(task.lease_until)),
          detailRow(t('lastHeartbeat'), dateTime(task.last_heartbeat_at)),
          detailRow(t('availableAt'), dateTime(task.available_at)),
          detailRow(t('nextRetryAt'), dateTime(task.next_retry_at)),
          detailRow(t('queueDuration'), durationMS(task.queue_duration_ms)),
          detailRow(t('executionDuration'), durationMS(task.execution_duration_ms)),
          detailRow(t('totalDuration'), durationMS(task.total_duration_ms))
        ])}
        ${detailSection(t('sectionExecution'), [
          detailRow(t('progress'), `${task.progress}%`),
          detailRow(t('retryCount'), `${task.retry_count} / ${task.max_retries}`),
          detailReferenceRow(t('resultRef'), task.result_ref),
          detailReferenceRow(t('inputReference'), input)
        ])}
        ${detailSection(t('sectionFailure'), [
          detailRow(t('errorCode'), task.error_code || t('none')),
          detailRow(t('retryable'), task.retryable === undefined ? t('none') : (task.retryable ? t('yes') : t('no'))),
          detailRow(t('errorLabel'), task.error_message || t('none'))
        ])}
        <details><summary>${escapeHTML(t('rawJSON'))}</summary><pre>${escapeHTML(JSON.stringify(rawTask, null, 2))}</pre></details>
      </div>
      <h2 class="events-title">${escapeHTML(t('events'))}</h2>
      ${eventList}`;

    document.getElementById('clear-selected-task')?.addEventListener('click', clearSelection);
    document.getElementById('cancel-selected-task')?.addEventListener('click', () => cancelTask(task.id));
  } catch (error) {
    if (requestToken !== detailRequestToken) return;
    target.className = 'empty';
    target.textContent = `${t('lookupFailed')}: ${error.message}`;
  }
}

async function cancelTask(taskID) {
  if (!confirm(t('cancelConfirm').replace('{id}', taskID))) return;
  try {
    const response = await fetch(`/tasks/${encodeURIComponent(taskID)}/cancel`, { method: 'POST' });
    if (!response.ok) {
      const body = await response.json().catch(() => ({}));
      throw new Error(body.error || `HTTP ${response.status}`);
    }
    await refreshAll(true);
  } catch (error) {
    alert(`${t('cancelFailed')}: ${error.message}`);
  }
}

function syncFilterControls() {
  document.getElementById('status-filter').value = filters.status;
  document.getElementById('workflow-filter').value = filters.workflow;
  document.querySelectorAll('.metric').forEach(card => {
    card.classList.toggle('active', card.dataset.status === filters.status);
  });
  updateScopeLabels();
}

function applyFiltersFromControls() {
  filters.status = document.getElementById('status-filter').value;
  filters.workflow = document.getElementById('workflow-filter').value.trim();
  resetPagination();
  syncFilterControls();
  refreshAll(true);
}

function toggleStatusFilter(status) {
  filters.status = filters.status === status ? '' : status;
  filters.workflow = document.getElementById('workflow-filter').value.trim();
  resetPagination();
  syncFilterControls();
  refreshAll(true);
  document.getElementById('task-list').scrollIntoView({ behavior: 'smooth', block: 'start' });
}

async function refreshAll(forceDetail) {
  if (document.hidden && !forceDetail) return;
  const operations = [refreshStats(), refreshRuntimeMetrics(), loadTasks(false)];
  if (selectedTaskID) operations.push(inspectTask(selectedTaskID, forceDetail));
  const results = await Promise.allSettled(operations);
  const failed = results.find(result => result.status === 'rejected');
  document.getElementById('refresh-status').textContent = failed
    ? `${t('metricsUnavailable')}: ${failed.reason.message}`
    : `${t('updated')} ${new Date().toLocaleTimeString(locale === 'zh' ? 'zh-CN' : 'en-US')}`;
}

function autoRefreshTick() {
  if (!document.getElementById('auto-refresh').checked) {
    document.getElementById('refresh-status').textContent = t('autoOff');
    return;
  }
  if (document.hidden) {
    document.getElementById('refresh-status').textContent = t('pagePaused');
    return;
  }
  refreshAll(false);
}

document.getElementById('runtime-scope')?.removeAttribute('data-i18n');
document.getElementById('lang-zh').addEventListener('click', () => {
  locale = 'zh';
  localStorage.setItem('taskpulse.locale', locale);
  applyLocale();
});
document.getElementById('lang-en').addEventListener('click', () => {
  locale = 'en';
  localStorage.setItem('taskpulse.locale', locale);
  applyLocale();
});
document.getElementById('refresh-all').addEventListener('click', () => refreshAll(true));
document.getElementById('auto-refresh').addEventListener('change', event => {
  if (event.target.checked) refreshAll(true);
  else document.getElementById('refresh-status').textContent = t('autoOff');
});
document.getElementById('task-form').addEventListener('submit', event => {
  event.preventDefault();
  selectTask(document.getElementById('task-id').value.trim());
});
document.getElementById('apply-filter').addEventListener('click', applyFiltersFromControls);
document.getElementById('clear-filter').addEventListener('click', () => {
  document.getElementById('status-filter').value = '';
  document.getElementById('workflow-filter').value = '';
  applyFiltersFromControls();
});
document.getElementById('status-filter').addEventListener('change', applyFiltersFromControls);
document.getElementById('workflow-filter').addEventListener('keydown', event => {
  if (event.key === 'Enter') {
    event.preventDefault();
    applyFiltersFromControls();
  }
});
document.querySelectorAll('.metric').forEach(card => {
  card.addEventListener('click', () => toggleStatusFilter(card.dataset.status));
});
document.getElementById('next-page').addEventListener('click', () => {
  if (!listState.nextCursor) return;
  listState.history.push(listState.cursor);
  listState.cursor = listState.nextCursor;
  loadTasks(true);
});
document.getElementById('previous-page').addEventListener('click', () => {
  if (!listState.history.length) return;
  listState.cursor = listState.history.pop();
  loadTasks(true);
});
document.addEventListener('visibilitychange', () => {
  if (!document.hidden && document.getElementById('auto-refresh').checked) refreshAll(true);
  else if (document.hidden) document.getElementById('refresh-status').textContent = t('pagePaused');
});
document.addEventListener('keydown', event => {
  if (event.key === 'Escape' && selectedTaskID && event.target.tagName !== 'INPUT') {
    clearSelection();
  }
});

applyLocale();
setInterval(autoRefreshTick, 5000);
